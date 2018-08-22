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
	"bufio"
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync"

	"github.com/google/go-github/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const cEnterMaintenance float64 = 1
const cLeaveMaintenance float64 = 0

var (
	fListenAddress string // The interface and port to listen on.
	fStateFilePath string // The filesystem path to write the maintenance state file.

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
			Name: "gmx_machine_state",
			Help: "State of machine.",
		},
		[]string{
			"machine",
			"issue",
		},
	)
	// Prometheus metric for exposing site maintenance status.
	metricSite = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_site_state",
			Help: "State of site.",
		},
		[]string{
			"site",
			"issue",
		},
	)

	state = maintenanceState{
		Machines: make(map[string]string),
		Sites:    make(map[string]string),
	}
)

// maintenanceState is a struct for storing both machine and site maintenance states.
type maintenanceState struct {
	Machines, Sites map[string]string
}

// writeState serializes the content of a maintenanceState object into JSON and
// writes it to a file on disk.
func writeState(f *bufio.Writer, s *maintenanceState) error {
	data, err := json.MarshalIndent(s, "", "    ")
	if err != nil {
		log.Printf("ERROR: Failed to marshal JSON: %s", err)
		metricError.WithLabelValues("marshaljson", "writeState").Add(1)
		return err
	}

	_, err = f.Write(data)
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
func restoreState(r *bufio.Reader, s *maintenanceState) error {
	data, err := ioutil.ReadAll(r)
	if err != nil {
		log.Printf("ERROR: Failed to read state data from %s: %s", fStateFilePath, err)
		metricError.WithLabelValues("readfile", "restoreState").Add(1)
		return err
	}

	err = json.Unmarshal(data, &s)
	if err != nil {
		log.Printf("ERROR: Failed to unmarshal JSON: %s", err)
		metricError.WithLabelValues("unmarshaljson", "restoreState").Add(1)
		return err
	}

	// Restore machine maintenance state.
	for machine, issue := range s.Machines {
		metricMachine.WithLabelValues(machine, issue).Set(cEnterMaintenance)
	}

	// Restore site maintenance state.
	for site, issue := range state.Sites {
		metricSite.WithLabelValues(site, issue).Set(cEnterMaintenance)
	}

	log.Printf("INFO: Successfully restored %s from disk.", fStateFilePath)
	return nil
}

// closeIssue removes any machines and sites from maintenance mode when the
// issue that added them to maintenance mode is closed. The return value is the
// number of modifications that were made to the machine and site maintenance
// state.
func closeIssue(issueNumber string, s *maintenanceState) int {
	var mods = 0
	// Remove any machines from maintenance that were set by this issue.
	for machine, issue := range s.Machines {
		if issue == issueNumber {
			delete(s.Machines, machine)
			metricMachine.WithLabelValues(machine+".measurement-lab.org", issueNumber).Set(0)
			log.Printf("INFO: Machine %s was removed from maintenance because issue was closed.", machine)
			mods++
		}
	}

	// Remove any sites from maintenance that were set by this issue.
	for site, issue := range s.Sites {
		if issue == issueNumber {
			delete(s.Sites, site)
			metricSite.WithLabelValues(site, issueNumber).Set(0)
			log.Printf("INFO: Site %s was removed from maintenance because issue was closed.", site)
			mods++
		}
	}

	return mods
}

// updateState modifies the maintenance state of a machine or site in the
// in-memory map as well as updating the Prometheus metric.
func updateState(stateMap map[string]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action float64) {
	mux.Lock()
	defer mux.Unlock()

	switch action {
	case cLeaveMaintenance:
		delete(stateMap, mapKey)
		metricState.WithLabelValues(mapKey, issueNumber).Set(action)
		log.Printf("INFO: Machine %s was removed from maintenance.", mapKey)
	case cEnterMaintenance:
		stateMap[mapKey] = issueNumber
		metricState.WithLabelValues(mapKey, issueNumber).Set(action)
		log.Printf("INFO: %s was added to maintenance.", mapKey)
	default:
		log.Printf("WARNING: Unknown action type: %f", action)
	}
}

// parseMessage scans the body of an issue or comment looking for special flags
// that match predefined patterns indicating that machine or site should be
// added to or removed from maintenance mode. If any matches are found, it
// updates the state for the item, then writes the entire state to disk. The
// return value is the number of modifications that were made to the machine and
// site maintenance state.
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

// receiveHook is the handler function for received webhooks. It validates the
// hook, parses the payload, makes sure that the hook event matches at least one
// event this exporter handles, then passes off the payload to parseMessage.
func receiveHook(resp http.ResponseWriter, req *http.Request) {
	var issueNumber string
	var status = http.StatusOK

	var StateMods = 0 // Number of modifications made to current state by webhook.

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
		if event.GetAction() == "closed" {
			log.Printf("INFO: Issue #%s was closed.", issueNumber)
			StateMods = closeIssue(issueNumber, &state)
		} else {
			StateMods = parseMessage(event.Issue.GetBody(), issueNumber, &state)
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Webhook is an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		StateMods = parseMessage(event.Comment.GetBody(), issueNumber, &state)
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
	if StateMods > 0 {
		mux.Lock()
		stateFile, err := os.Create(fStateFilePath)
		if err != nil {
			log.Printf("ERROR: Failed to create state file %s: %s", fStateFilePath, err)
			metricError.WithLabelValues("createfile", "writeState").Add(1)
		}
		defer stateFile.Close()
		writer := bufio.NewWriter(stateFile)
		err = writeState(writer, &state)
		if err != nil {
			log.Printf("ERROR: failed to write state file %s: %s", fStateFilePath, err)
			metricError.WithLabelValues("writefile", "receiveHook").Add(1)
		}
		mux.Unlock()
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
	}
	defer stateFile.Close()

	restoreState(bufio.NewReader(stateFile), &state)

	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fListenAddress, nil))
}
