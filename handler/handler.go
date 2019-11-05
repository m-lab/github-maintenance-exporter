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
	"github.com/prometheus/client_golang/prometheus"
)

var (
	machineRegExps = map[string]*regexp.Regexp{
		"mlab-sandbox": regexp.MustCompile(`\/machine\s+(mlab[1-4]\.[a-z]{3}[0-9]t)(\s+del)?`),
		"mlab-staging": regexp.MustCompile(`\/machine\s+(mlab[4]\.[a-z]{3}[0-9c]{2})(\s+del)?`),
		"mlab-oti":     regexp.MustCompile(`\/machine\s+(mlab[1-3]\.[a-z]{3}[0-9c]{2})(\s+del)?`),
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

// Looks for a string a slice.
func stringInSlice(s string, list []string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// Removes a single issue from a site/machine. If the issue was the last one
// associated with the site/machine, it will also remove the site/machine
// from maintenance.
func (h *handler) removeIssue(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string) int {

	var mods = 0
	mapElement := stateMap[mapKey]

	issueIndex := stringInSlice(issueNumber, mapElement)
	if issueIndex >= 0 {
		mapElement[issueIndex] = mapElement[len(mapElement)-1]
		mapElement = mapElement[:len(mapElement)-1]
		if len(mapElement) == 0 {
			delete(stateMap, mapKey)
			// If this is a machine state, then we need to pass mapKey twice, once for the
			// "machine" label and once for the "node" label.
			if strings.HasPrefix(mapKey, "mlab") {
				metricState.WithLabelValues(mapKey, mapKey).Set(0)
			} else {
				metricState.WithLabelValues(mapKey).Set(0)
			}
		} else {
			stateMap[mapKey] = mapElement
		}
		log.Printf("INFO: %s was removed from maintenance for issue #%s", mapKey, issueNumber)
		mods++
	}
	return mods
}

// closeIssue removes any machines and sites from maintenance mode when the
// issue that added them to maintenance mode is closed. The return value is the
// number of modifications that were made to the machine and site maintenance
// state.
func (h *handler) closeIssue(issueNumber string, s *maintenancestate.MaintenanceState) int {
	var totalMods = 0
	// Remove any sites from maintenance that were set by this issue.
	for site, issues := range s.Sites {
		issueIndex := stringInSlice(issueNumber, issues)
		if issueIndex >= 0 {
			mods := h.removeIssue(s.Sites, site, metrics.Site, issueNumber)
			totalMods = totalMods + mods
			// Since site is leaving maintenance, remove all associated machine maintenances.
			for _, num := range []string{"1", "2", "3", "4"} {
				machine := "mlab" + num + "." + site + ".measurement-lab.org"
				mods := h.removeIssue(s.Machines, machine, metrics.Machine, issueNumber)
				totalMods = totalMods + mods
			}
		}
	}

	// Remove any machines from maintenance that were set by this issue.
	for machine, issues := range s.Machines {
		issueIndex := stringInSlice(issueNumber, issues)
		if issueIndex >= 0 {
			mods := h.removeIssue(s.Machines, machine, metrics.Machine, issueNumber)
			totalMods = totalMods + mods
		}
	}

	return totalMods
}

// updateState modifies the maintenance state of a machine or site in the
// in-memory map as well as updating the Prometheus metric.
func (h *handler) updateState(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action maintenancestate.Action) {

	switch action {
	case maintenancestate.LeaveMaintenance:
		h.removeIssue(stateMap, mapKey, metricState, issueNumber)
	case maintenancestate.EnterMaintenance:
		// Don't enter maintenance more than once for a given issue.
		issueIndex := stringInSlice(issueNumber, stateMap[mapKey])
		if issueIndex >= 0 {
			log.Printf("INFO: %s is already in maintenance for issue #%s", mapKey, issueNumber)
			return
		}
		stateMap[mapKey] = append(stateMap[mapKey], issueNumber)
		// If this is a machine state, then we need to pass mapKey twice, once for the
		// "machine" label and once for the "node" label.
		if strings.HasPrefix(mapKey, "mlab") {
			metricState.WithLabelValues(mapKey, mapKey).Set(float64(action))
		} else {
			metricState.WithLabelValues(mapKey).Set(float64(action))
		}
		log.Printf("INFO: %s was added to maintenance for issue #%s", mapKey, issueNumber)
	default:
		log.Printf("WARNING: Unknown action type: %f", action)
	}
}

// parseMessage scans the body of an issue or comment looking for special flags
// that match predefined patterns indicating that machine or site should be
// added to or removed from maintenance mode. If any matches are found, it
// updates the state for the item. The return value is the number of
// modifications that were made to the machine and site maintenance state.
func (h *handler) parseMessage(msg string, issueNumber string, s *maintenancestate.MaintenanceState, project string) int {
	var mods = 0
	siteMatches := siteRegExps[project].FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if strings.TrimSpace(site[2]) == "del" {
				h.updateState(s.Sites, site[1], metrics.Site, issueNumber, maintenancestate.LeaveMaintenance)
				mods++
				// Since site is leaving maintenance, remove all associated machine maintenances.
				for _, num := range []string{"1", "2", "3", "4"} {
					machine := "mlab" + num + "." + site[1] + ".measurement-lab.org"
					h.updateState(s.Machines, machine, metrics.Machine, issueNumber, maintenancestate.LeaveMaintenance)
					mods++
				}
			} else {
				h.updateState(s.Sites, site[1], metrics.Site, issueNumber, maintenancestate.EnterMaintenance)
				mods++
				// Since site is entering maintenance, add all associated machine maintenances.
				for _, num := range []string{"1", "2", "3", "4"} {
					machine := "mlab" + num + "." + site[1] + ".measurement-lab.org"
					h.updateState(s.Machines, machine, metrics.Machine, issueNumber, maintenancestate.EnterMaintenance)
					mods++
				}
			}
		}
	}

	machineMatches := machineRegExps[project].FindAllStringSubmatch(msg, -1)
	if len(machineMatches) > 0 {
		for _, machine := range machineMatches {
			log.Printf("INFO: Flag found for machine: %s", machine[1])
			label := machine[1] + ".measurement-lab.org"
			if strings.TrimSpace(machine[2]) == "del" {
				h.updateState(s.Machines, label, metrics.Machine, issueNumber, maintenancestate.LeaveMaintenance)
				mods++
			} else {
				h.updateState(s.Machines, label, metrics.Machine, issueNumber, maintenancestate.EnterMaintenance)
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
	// Multithreaded map access is complicated, so for now we just guard
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
			mods = h.closeIssue(issueNumber, h.state)
		case "opened", "edited":
			mods = h.parseMessage(event.Issue.GetBody(), issueNumber, h.state, h.project)
		default:
			log.Printf("INFO: Unsupported IssueEvent action: %s.", eventAction)
			status = http.StatusNotImplemented
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Webhook is an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		issueState := event.Issue.GetState()
		if issueState == "open" {
			mods = h.parseMessage(event.Comment.GetBody(), issueNumber, h.state, h.project)
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
			return
		}
	}

	resp.WriteHeader(status)
	return
}

func New(state *maintenancestate.MaintenanceState, githubSecret []byte, project string) http.Handler {
	return &handler{
		state:        state,
		githubSecret: githubSecret,
		project:      project,
	}
}
