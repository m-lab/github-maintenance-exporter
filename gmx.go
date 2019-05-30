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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"github.com/google/go-github/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const cEnterMaintenance float64 = 1
const cLeaveMaintenance float64 = 0

var (
	fListenAddress    string // Interface and port to listen on.
	fStateFilePath    string // Filesystem path to write the maintenance state file.
	fGitHubSecretPath string // Filesystem path to file which contains the shared Github secret.

	githubSecret []byte // The symetric secret used to validate that the webhook actually came from Github.

	mux sync.Mutex

	machineRegExp = regexp.MustCompile(`\/machine (mlab[1-4]{1}\.[a-z]{3}[0-9c]{2})\s?(del)?`)
	siteRegExp    = regexp.MustCompile(`\/site ([a-z]{3}[0-9c]{2})\s?(del)?`)

	// Prometheus metric for exposing any errors that the exporter encounters.
	metricError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gmx_error_count",
			Help: "Count of errors.",
		},
		[]string{
			"type",
			"function",
		},
	)
	// Prometheus metric for exposing machine maintenance status.
	metricMachine = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_machine_maintenance",
			Help: "Whether a machine is in maitenance mode or not.",
		},
		[]string{
			"machine",
		},
	)
	// Prometheus metric for exposing site maintenance status.
	metricSite = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_site_maintenance",
			Help: "Whether a site is in maintenance mode or not.",
		},
		[]string{
			"site",
		},
	)

	state = maintenanceState{
		Machines: make(map[string][]string),
		Sites:    make(map[string][]string),
	}
)

// maintenanceState is a struct for storing both machine and site maintenance states.
type maintenanceState struct {
	Machines, Sites map[string][]string
}

// writeState serializes the content of a maintenanceState object into JSON and
// writes it to a file on disk.
func writeState(w io.Writer, s *maintenanceState) error {
	data, err := json.MarshalIndent(s, "", "    ")
	if err != nil {
		log.Printf("ERROR: Failed to marshal JSON: %s", err)
		metricError.WithLabelValues("marshaljson", "writeState").Add(1)
		return err
	}

	_, err = w.Write(data)
	if err != nil {
		log.Printf("ERROR: Failed to write state to %s: %s", fStateFilePath, err)
		metricError.WithLabelValues("writefile", "writeState").Add(1)
		return err
	}

	log.Printf("INFO: Successfully wrote state to %s.", fStateFilePath)
	return nil
}

// restoreState reads serialized JSON data from disk and loads it into
// maintenanceState object.
func restoreState(r io.Reader, s *maintenanceState) error {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		log.Printf("ERROR: Failed to read state data from %s: %s", fStateFilePath, err)
		metricError.WithLabelValues("readfile", "restoreState").Inc()
		return err
	}

	err = json.Unmarshal(data, &s)
	if err != nil {
		log.Printf("ERROR: Failed to unmarshal JSON: %s", err)
		metricError.WithLabelValues("unmarshaljson", "restoreState").Inc()
		return err
	}

	// Restore machine maintenance state.
	for machine := range s.Machines {
		metricMachine.WithLabelValues(machine).Set(cEnterMaintenance)
	}

	// Restore site maintenance state.
	for site := range state.Sites {
		metricSite.WithLabelValues(site).Set(cEnterMaintenance)
	}

	log.Printf("INFO: Successfully restored %s from disk.", fStateFilePath)
	return nil
}

func removeIssue(stateMap map[string][]string, metricState *prometheus.GaugeVec,
	issueNumber string) int {
	var mods = 0
	for key, issues := range stateMap {
		issueIndex := sort.SearchStrings(issues, issueNumber)
		// If an element doesn't exist sort.SearchStrings will return the index
		// where the search item could be inserted in the slice, which will be
		// equivalent to len(slice). Therefore, if the index returned is less
		// than len(slice) we can infer the element was found.
		if issueIndex < len(issues) {
			// Overwrites the element we want to remove with the value of the
			// last element, then remove the last element. Apparently this is
			// faster than other methods for removing an element of a slice.
			issues[issueIndex] = issues[len(issues)-1]
			issues = issues[:len(issues)-1]
			if len(issues) == 0 {
				delete(stateMap, key)
				metricState.WithLabelValues(key).Set(0)
				log.Printf("INFO: %s was removed from maintenance.", key)
			} else {
				stateMap[key] = issues
			}
			mods++
		}
	}
	return mods
}

// closeIssue removes any machines and sites from maintenance mode when the
// issue that added them to maintenance mode is closed. The return value is the
// number of modifications that were made to the machine and site maintenance
// state.
func closeIssue(issueNumber string, s *maintenanceState) int {
	var mods = 0
	// Remove any machines from maintenance that were set by this issue.
	machineMods := removeIssue(s.Machines, metricMachine, issueNumber)
	mods = mods + machineMods

	// Remove any sites from maintenance that were set by this issue.
	siteMods := removeIssue(s.Sites, metricSite, issueNumber)
	mods = mods + siteMods

	return mods
}

// updateState modifies the maintenance state of a machine or site in the
// in-memory map as well as updating the Prometheus metric.
func updateState(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action float64) {
	mux.Lock()
	defer mux.Unlock()

	switch action {
	case cLeaveMaintenance:
		removeIssue(stateMap, metricState, issueNumber)
	case cEnterMaintenance:
		stateMap[mapKey] = append(stateMap[mapKey], issueNumber)
		metricState.WithLabelValues(mapKey).Set(action)
		log.Printf("INFO: %s was added to maintenance.", mapKey)
	default:
		log.Printf("WARNING: Unknown action type: %f", action)
	}
}

// parseMessage scans the body of an issue or comment looking for special flags
// that match predefined patterns indicating that machine or site should be
// added to or removed from maintenance mode. If any matches are found, it
// updates the state for the item. The return value is the number of
// modifications that were made to the machine and site maintenance state.
func parseMessage(msg string, issueNumber string, s *maintenanceState) int {
	var mods = 0
	machineMatches := machineRegExp.FindAllStringSubmatch(msg, -1)
	if len(machineMatches) > 0 {
		for _, machine := range machineMatches {
			log.Printf("INFO: Flag found for machine: %s", machine[1])
			label := machine[1] + ".measurement-lab.org"
			if machine[2] == "del" {
				updateState(s.Machines, label, metricMachine, issueNumber, cLeaveMaintenance)
				mods++
			} else {
				updateState(s.Machines, label, metricMachine, issueNumber, cEnterMaintenance)
				mods++
			}
		}
	}

	siteMatches := siteRegExp.FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if site[2] == "del" {
				updateState(s.Sites, site[1], metricSite, issueNumber, 0)
				mods++
			} else {
				updateState(s.Sites, site[1], metricSite, issueNumber, 1)
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
	fmt.Fprintf(resp, "GitHub Maintenance Exporter")
	resp.WriteHeader(http.StatusOK)
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
		metricError.WithLabelValues("validatehook", "receiveHook").Add(1)
		resp.WriteHeader(http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)
	if err != nil {
		log.Printf("ERROR: Failed to parse webhook with error: %s", err)
		metricError.WithLabelValues("parsehook", "receiveHook").Add(1)
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
			mods = closeIssue(issueNumber, &state)
		case "opened", "edited":
			mods = parseMessage(event.Issue.GetBody(), issueNumber, &state)
		default:
			log.Printf("INFO: Unsupported IssueEvent action: %s.", eventAction)
			status = http.StatusNotImplemented
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Webhook is an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		issueState := event.Issue.GetState()
		if issueState == "open" {
			mods = parseMessage(event.Comment.GetBody(), issueNumber, &state)
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

		stateFile, err := os.Create(fStateFilePath)
		if err != nil {
			log.Printf("ERROR: Failed to create state file %s: %s", fStateFilePath, err)
			metricError.WithLabelValues("createfile", "writeState").Add(1)
			return
		}
		defer stateFile.Close()
		err = writeState(stateFile, &state)
		if err != nil {
			log.Printf("ERROR: failed to write state file %s: %s", fStateFilePath, err)
			metricError.WithLabelValues("writefile", "receiveHook").Add(1)
			return
		}
	}

	resp.WriteHeader(status)
	return
}

// init initializes the Prometheus metrics and drops any passed flags into
// global variables.
func init() {
	flag.StringVar(&fListenAddress, "web.listen-address", ":9999",
		"Address to listen on for telemetry.")
	flag.StringVar(&fStateFilePath, "storage.state-file", "/tmp/gmx-state",
		"Filesystem path for the state file.")
	flag.StringVar(&fGitHubSecretPath, "storage.github-secret", "",
		"Filesystem path of file containing the shared Github webhook secret.")
	prometheus.MustRegister(metricError)
	prometheus.MustRegister(metricMachine)
	prometheus.MustRegister(metricSite)
}

func main() {
	flag.Parse()

	stateFile, err := os.Open(fStateFilePath)
	if err != nil {
		log.Printf("WARNING: Failed to open state file %s: %s", fStateFilePath, err)
		metricError.WithLabelValues("openfile", "main").Add(1)
	} else {
		restoreState(stateFile, &state)
	}
	stateFile.Close()

	// If provided, read the GitHub shared webhook secret from a file, else expect to
	// find it in the environment.
	if fGitHubSecretPath != "" {
		secretFile, err := os.Open(fGitHubSecretPath)
		if err != nil {
			log.Printf("ERROR: Failed to open secret file %s: %s", fGitHubSecretPath, err)
			os.Exit(1)
		}
		secret, err := ioutil.ReadAll(secretFile)
		if err != nil {
			log.Printf("ERROR: Failed to read secret file %s: %s", fGitHubSecretPath, err)
			os.Exit(1)
		}
		secretTrimmed := bytes.TrimSpace(secret)
		if len(secretTrimmed) == 0 {
			log.Printf("ERROR: Github secret file %s is empty.", fGitHubSecretPath)
			os.Exit(1)
		}
		githubSecret = secretTrimmed
		secretFile.Close()
	} else {
		githubSecret = []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	}

	if len(githubSecret) == 0 {
		log.Fatal("ERROR: No GitHub webhook secret found.")
	}

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fListenAddress, nil))
}
