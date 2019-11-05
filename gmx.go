// Copyright 2018 github-maintenance-exporter Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//////////////////////////////////////////////////////////////////////////////

// A simple Prometheus exporter that receives Github webhooks for issues and
// issue_comments events and parses the issue or comment body for special flags
// that indicate that a machine or site should be in maintenace mode.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/google/go-github/github"
	"github.com/m-lab/github-maintenance-exporter/maintenancestate"
	"github.com/m-lab/github-maintenance-exporter/metrics"
	"github.com/m-lab/go/rtx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const cEnterMaintenance float64 = 1
const cLeaveMaintenance float64 = 0

var (
	fListenAddress    = flag.String("web.listen-address", ":9999", "Address to listen on for telemetry.")
	fStateFilePath    = flag.String("storage.state-file", "/tmp/gmx-state", "Filesystem path for the state file.")
	fGitHubSecretPath = flag.String("storage.github-secret", "", "Filesystem path of file containing the shared Github webhook secret.")
	fProject          = flag.String("project", "mlab-oti", "GCP project where this instance is running.")

	githubSecret []byte // The symetric secret used to validate that the webhook actually came from Github.

	mux sync.Mutex

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

	state *maintenancestate.MaintenanceState
)

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
func removeIssue(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string) int {
	mux.Lock()
	defer mux.Unlock()

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
func closeIssue(issueNumber string, s *maintenancestate.MaintenanceState) int {
	var totalMods = 0
	// Remove any sites from maintenance that were set by this issue.
	for site, issues := range s.Sites {
		issueIndex := stringInSlice(issueNumber, issues)
		if issueIndex >= 0 {
			mods := removeIssue(s.Sites, site, metrics.Site, issueNumber)
			totalMods = totalMods + mods
			// Since site is leaving maintenance, remove all associated machine maintenances.
			for _, num := range []string{"1", "2", "3", "4"} {
				machine := "mlab" + num + "." + site + ".measurement-lab.org"
				mods := removeIssue(s.Machines, machine, metrics.Machine, issueNumber)
				totalMods = totalMods + mods
			}
		}
	}

	// Remove any machines from maintenance that were set by this issue.
	for machine, issues := range s.Machines {
		issueIndex := stringInSlice(issueNumber, issues)
		if issueIndex >= 0 {
			mods := removeIssue(s.Machines, machine, metrics.Machine, issueNumber)
			totalMods = totalMods + mods
		}
	}

	return totalMods
}

// updateState modifies the maintenance state of a machine or site in the
// in-memory map as well as updating the Prometheus metric.
func updateState(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action float64) {

	switch action {
	case cLeaveMaintenance:
		removeIssue(stateMap, mapKey, metricState, issueNumber)
	case cEnterMaintenance:
		// Don't enter maintenance more than once for a given issue.
		issueIndex := stringInSlice(issueNumber, stateMap[mapKey])
		if issueIndex >= 0 {
			log.Printf("INFO: %s is already in maintenance for issue #%s", mapKey, issueNumber)
			return
		}
		mux.Lock()
		stateMap[mapKey] = append(stateMap[mapKey], issueNumber)
		// If this is a machine state, then we need to pass mapKey twice, once for the
		// "machine" label and once for the "node" label.
		if strings.HasPrefix(mapKey, "mlab") {
			metricState.WithLabelValues(mapKey, mapKey).Set(action)
		} else {
			metricState.WithLabelValues(mapKey).Set(action)
		}
		log.Printf("INFO: %s was added to maintenance for issue #%s", mapKey, issueNumber)
		mux.Unlock()
	default:
		log.Printf("WARNING: Unknown action type: %f", action)
	}
}

// parseMessage scans the body of an issue or comment looking for special flags
// that match predefined patterns indicating that machine or site should be
// added to or removed from maintenance mode. If any matches are found, it
// updates the state for the item. The return value is the number of
// modifications that were made to the machine and site maintenance state.
func parseMessage(msg string, issueNumber string, s *maintenancestate.MaintenanceState, project string) int {
	var mods = 0
	siteMatches := siteRegExps[project].FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if strings.TrimSpace(site[2]) == "del" {
				updateState(s.Sites, site[1], metrics.Site, issueNumber, cLeaveMaintenance)
				mods++
				// Since site is leaving maintenance, remove all associated machine maintenances.
				for _, num := range []string{"1", "2", "3", "4"} {
					machine := "mlab" + num + "." + site[1] + ".measurement-lab.org"
					updateState(s.Machines, machine, metrics.Machine, issueNumber, cLeaveMaintenance)
					mods++
				}
			} else {
				updateState(s.Sites, site[1], metrics.Site, issueNumber, cEnterMaintenance)
				mods++
				// Since site is entering maintenance, add all associated machine maintenances.
				for _, num := range []string{"1", "2", "3", "4"} {
					machine := "mlab" + num + "." + site[1] + ".measurement-lab.org"
					updateState(s.Machines, machine, metrics.Machine, issueNumber, cEnterMaintenance)
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
				updateState(s.Machines, label, metrics.Machine, issueNumber, cLeaveMaintenance)
				mods++
			} else {
				updateState(s.Machines, label, metrics.Machine, issueNumber, cEnterMaintenance)
				mods++
			}
		}
	}

	return mods
}

// rootHandler implements the simplest possible handler for root requests,
// simply printing the name of the utility and returning a 200 status. This
// could be used by, for example, kubernetes aliveness checks.
func rootHandler(resp http.ResponseWriter, req *http.Request) {
	resp.WriteHeader(http.StatusOK)
	fmt.Fprintf(resp, "GitHub Maintenance Exporter")
	return
}

// receiveHook is the handler function for received webhooks. It validates the
// hook, parses the payload, makes sure that the hook event matches at least one
// event this exporter handles, then passes off the payload to parseMessage.
func receiveHook(resp http.ResponseWriter, req *http.Request) {
	var issueNumber string
	var mods = 0 // Number of modifications made to current state by webhook.
	var status = http.StatusOK

	log.Println("INFO: Received a webhook.")

	payload, err := github.ValidatePayload(req, githubSecret)
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
			mods = closeIssue(issueNumber, state)
		case "opened", "edited":
			mods = parseMessage(event.Issue.GetBody(), issueNumber, state, *fProject)
		default:
			log.Printf("INFO: Unsupported IssueEvent action: %s.", eventAction)
			status = http.StatusNotImplemented
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Webhook is an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		issueState := event.Issue.GetState()
		if issueState == "open" {
			mods = parseMessage(event.Comment.GetBody(), issueNumber, state, *fProject)
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
		mux.Lock()
		defer mux.Unlock()

		err = state.Write()
		if err != nil {
			log.Printf("ERROR: failed to write state file: %s", err)
			metrics.Error.WithLabelValues("writefile", "receiveHook").Add(1)
			return
		}
	}

	resp.WriteHeader(status)
	return
}

// MustReadGithubSecret reads the GitHub shared webhook secret from a file (if a
// filename is provided) or retrieves it from the environment. It exits with a
// fatal error if the secret is not found or is bad for any reason.
func MustReadGithubSecret(filename string) []byte {
	var secret []byte

	// Read it from a file or the environment.
	if filename != "" {
		var err error
		secret, err = ioutil.ReadFile(filename)
		rtx.Must(err, "ERROR: Could not read file %s", filename)
	} else {
		secret = []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	}

	secretTrimmed := bytes.TrimSpace(secret)
	if len(secretTrimmed) == 0 {
		log.Fatal("ERROR: Github secret is empty.")
	}
	return secretTrimmed
}

func main() {
	flag.Parse()

	state = maintenancestate.New(*fStateFilePath)
	err := state.Restore()
	if err != nil {
		log.Printf("WARNING: Failed to open state file %s: %s", *fStateFilePath, err)
		metrics.Error.WithLabelValues("openfile", "main").Add(1)
	}

	githubSecret = MustReadGithubSecret(*fGitHubSecretPath)

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(*fListenAddress, nil))
}
