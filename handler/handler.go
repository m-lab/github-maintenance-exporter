// Package handler contains all the code that parses an incoming web request
// (likely from github's web hooks).
package handler

import (
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-github/github"
	"github.com/m-lab/github-maintenance-exporter/maintenancestate"
	"github.com/m-lab/github-maintenance-exporter/metrics"
)

var (
	machineRegExps = map[string]*regexp.Regexp{
		"mlab-sandbox": regexp.MustCompile(`\/machine\s+(mlab[1-4][.-][a-z]{3}[0-9]t)(\s+del)?`),
		"mlab-staging": regexp.MustCompile(`\/machine\s+(mlab[4][.-][a-z]{3}[0-9c]{2})(\s+del)?`),
		"mlab-oti":     regexp.MustCompile(`\/machine\s+(mlab[1-3][.-][a-z]{3}[0-9c]{2})(\s+del)?`),
	}

	siteRegExps = map[string]*regexp.Regexp{
		"mlab-sandbox": regexp.MustCompile(`\/site\s+([a-z]{3}[0-9]t)(\s+del)?`),
		"mlab-staging": regexp.MustCompile(`\/site\s+([a-z]{3}[0-9c]{2})(\s+del)?`),
		"mlab-oti":     regexp.MustCompile(`\/site\s+([a-z]{3}[0-9c]{2})(\s+del)?`),
	}
)

type handler struct {
	mux          sync.Mutex
	state        *maintenancestate.MaintenanceState
	githubSecret []byte
	project      string
}

// parseMessage scans the body of an issue or comment looking for special flags
// that match predefined patterns indicating that machine or site should be
// added to or removed from maintenance mode. If any matches are found, it
// updates the state for the item. The return value is the number of
// modifications that were made to the machine and site maintenance state.
func (h *handler) parseMessage(msg string, issueNumber string) int {
	var mods = 0
	siteMatches := siteRegExps[h.project].FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if strings.TrimSpace(site[2]) == "del" {
				mods += h.state.UpdateSite(site[1], maintenancestate.LeaveMaintenance, issueNumber, h.project)
			} else {
				mods += h.state.UpdateSite(site[1], maintenancestate.EnterMaintenance, issueNumber, h.project)
			}
		}
	}

	machineMatches := machineRegExps[h.project].FindAllStringSubmatch(msg, -1)
	if len(machineMatches) > 0 {
		for _, machine := range machineMatches {
			log.Printf("INFO: Flag found for machine: %s", machine[1])
			label := strings.Replace(machine[1], ".", "-", 1)
			if strings.TrimSpace(machine[2]) == "del" {
				h.state.UpdateMachine(label, maintenancestate.LeaveMaintenance, issueNumber, h.project)
				mods++
			} else {
				h.state.UpdateMachine(label, maintenancestate.EnterMaintenance, issueNumber, h.project)
				mods++
			}
		}
	}

	return mods
}

// ServeHTTP is the handler function for received webhooks. It validates the
// hook, parses the payload, makes sure that the hook event matches at least one
// event this exporter handles, then passes off the payload to parseMessage.
func (h *handler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// Multithreaded map access+mutation is complicated, so for now we just guard
	// everything with a global mutex.
	h.mux.Lock()
	defer h.mux.Unlock()
	var issueNumber string
	var mods = 0 // Number of modifications made to current state by webhook.
	var status = http.StatusOK

	log.Println("INFO: Received a webhook.")

	payload, err := github.ValidatePayload(req, h.githubSecret)
	if err != nil {
		log.Printf("ERROR: Validation of Webhook failed: %s", err)
		metrics.Error.WithLabelValues("validatehook", "receiveHook").Add(1)
		resp.WriteHeader(http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)
	if err != nil {
		log.Printf("ERROR: Failed to parse webhook with error: %s", err)
		metrics.Error.WithLabelValues("parsehook", "receiveHook").Add(1)
		resp.WriteHeader(http.StatusBadRequest)
		return
	}

	switch event := event.(type) {
	case *github.IssuesEvent:
		log.Println("INFO: Webhook is an Issues event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		eventAction := event.GetAction()
		switch eventAction {
		case "closed", "deleted":
			log.Printf("INFO: Issue #%s was %s.", issueNumber, eventAction)
			mods = h.state.CloseIssue(issueNumber, h.project)
		case "opened", "edited":
			mods = h.parseMessage(event.Issue.GetBody(), issueNumber)
		default:
			log.Printf("INFO: Unsupported IssueEvent action: %s.", eventAction)
			status = http.StatusNotImplemented
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Webhook is an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		issueState := event.Issue.GetState()
		if issueState == "open" {
			mods = h.parseMessage(event.Comment.GetBody(), issueNumber)
		} else {
			log.Printf("INFO: Ignoring IssueComment event on closed issue #%s.", issueNumber)
			status = http.StatusExpectationFailed
		}
	case *github.PingEvent:
		log.Println("INFO: Webhook is a Ping event.")
		var cnt = 0
		// Since this exporter only processes "issues" and "issue_comment" Github
		// webhook events, be sure that at least these two events are enabled for the
		// webhook.
		for _, v := range event.Hook.Events {
			if v == "issues" || v == "issue_comment" {
				cnt++
			}
		}
		if cnt != 2 {
			log.Printf("ERROR: Registered webhook events do not include both 'issues' and 'issue_comment'.")
			status = http.StatusExpectationFailed
		}
	default:
		log.Println("WARNING: Received unimplemented webhook event type.")
		status = http.StatusNotImplemented
	}

	// Only write state to file if the current state was modified.
	if mods > 0 {
		err = h.state.Write()
		if err != nil {
			log.Printf("ERROR: failed to write state file: %s", err)
			metrics.Error.WithLabelValues("writefile", "receiveHook").Add(1)
			status = http.StatusInternalServerError
		}
	}

	resp.WriteHeader(status)
}

// New creates an http.Handler for receiving github webhook events to update the maintenance state.
func New(state *maintenancestate.MaintenanceState, githubSecret []byte, project string) http.Handler {
	return &handler{
		state:        state,
		githubSecret: githubSecret,
		project:      project,
	}
}
