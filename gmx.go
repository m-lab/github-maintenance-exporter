// Copyright 2016 ePoxy Authors
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

	githubSecret = []byte("7f29588262f53e45ea1aa1da7e0f13b9105e6589") // The symetric secret used to validate that webhook actually came from Github.

	mux sync.Mutex

	machineRegExp = regexp.MustCompile(`\/machine (mlab[1-4]{1}\.[a-z]{3}[0-9c]{2})\s?(del)?`)
	siteRegExp    = regexp.MustCompile(`\/site ([a-z]{3}[0-9c]{2})\s?(del)?`)

	// The Prometheus metric for exposing machine maintenance status.
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
	// The Prometheus metric for exposing site maintenance status.
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

type maintenanceState struct {
	Machines, Sites map[string]string
}

func writeState() error {
	mux.Lock()
	defer mux.Unlock()

	stateFile, err := os.Create(fStateFilePath)
	if err != nil {
		log.Printf("ERROR: Failed to create state file %s: %s", fStateFilePath, err)
		return err
	}
	defer stateFile.Close()

	data, err := json.MarshalIndent(state, "", "    ")
	if err != nil {
		log.Printf("ERROR: Failed to marshal JSON: %s", err)
		return err
	}

	_, err = stateFile.Write(data)
	if err != nil {
		log.Printf("ERROR: Failed to write state to %s: %s", fStateFilePath, err)
		return err
	}

	log.Printf("INFO: Successfully wrote state to %s.", fStateFilePath)
	return nil
}

func restoreState() error {
	stateFile, err := os.Open(fStateFilePath)
	if err != nil {
		log.Printf("WARNING: Failed to open state file %s: %s", fStateFilePath, err)
		return err
	}
	defer stateFile.Close()

	data, err := ioutil.ReadAll(stateFile)
	if err != nil {
		log.Printf("ERROR: Failed to read state data from %s: %s", fStateFilePath, err)
		return err
	}

	err = json.Unmarshal(data, &state)
	if err != nil {
		log.Printf("ERROR: Failed to unmarshal JSON: %s", err)
		return err
	}

	// Restore machine maintenance state.
	for machine, issue := range state.Machines {
		metricMachine.WithLabelValues(machine, issue).Set(1)
	}

	// Restore site maintenance state.
	for site, issue := range state.Sites {
		metricSite.WithLabelValues(site, issue).Set(1)
	}

	log.Printf("INFO: Successfully restored %s from disk.", fStateFilePath)
	return nil
}

func closeIssue(issueNumber string) {
	// Remove any machines from maintenance that were set by this issue.
	for machine, issue := range state.Machines {
		if issue == issueNumber {
			delete(state.Machines, machine)
			metricMachine.WithLabelValues(machine+".measurement-lab.org", issueNumber).Set(0)
			log.Printf("INFO: Machine %s was removed from maintenance because issue was closed.", machine)
		}
	}

	// Remove any sites from maintenance that were set by this issue.
	for site, issue := range state.Sites {
		if issue == issueNumber {
			delete(state.Sites, site)
			metricSite.WithLabelValues(site, issueNumber).Set(0)
			log.Printf("INFO: Site %s was removed from maintenance because issue was closed.", site)
		}
	}

	writeState()
}

func updateState(stateMap map[string]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action float64) {
	mux.Lock()
	defer mux.Unlock()

	// Updates the state map and Prometheus metric for a machine or site
	switch action {
	case 0:
		delete(stateMap, mapKey)
		metricState.WithLabelValues(mapKey+".measurement-lab.org", issueNumber).Set(action)
		log.Printf("INFO: Machine %s was removed from maintenance.", mapKey)
	case 1:
		stateMap[mapKey] = issueNumber
		metricState.WithLabelValues(mapKey, issueNumber).Set(action)
		log.Printf("INFO: %s was added to maintenance.", mapKey)
	default:
		log.Printf("WARNING: Unknown action type: %f", action)
	}
}

func parseMessage(msg string, issueNumber string) {
	machineMatches := machineRegExp.FindAllStringSubmatch(msg, -1)
	if len(machineMatches) > 0 {
		for _, machine := range machineMatches {
			log.Printf("INFO: Flag found for machine: %s", machine[1])
			label := machine[1] + ".measurement-lab.org"
			if machine[2] == "del" {
				log.Printf("INFO: Machine %s will be removed from maintenance.", machine[1])
				updateState(state.Machines, label, metricMachine, issueNumber, cLeaveMaintenance)
			} else {
				log.Printf("INFO: Machine %s will be added to maintenance.", machine[1])
				updateState(state.Machines, label, metricMachine, issueNumber, cEnterMaintenance)
			}
		}
	}

	siteMatches := siteRegExp.FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if site[2] == "del" {
				log.Printf("INFO: Site %s will be removed from maintenance.", site[1])
				updateState(state.Sites, site[1], metricSite, issueNumber, 0)
			} else {
				log.Printf("INFO: Site %s will be added to maintenance.", site[1])
				updateState(state.Sites, site[1], metricSite, issueNumber, 1)
			}
		}
	}

	writeState()
}

func receiveHook(resp http.ResponseWriter, req *http.Request) {
	var issueNumber string
	var status = http.StatusOK

	log.Println("INFO: Received a webhook.")

	payload, err := github.ValidatePayload(req, githubSecret)
	if err != nil {
		log.Printf("ERROR: Validation of Webhook failed: %s", err)
		resp.WriteHeader(http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)
	if err != nil {
		log.Printf("ERROR: Failed to parse webhook with error: %s", err)
		resp.WriteHeader(http.StatusBadRequest)
		return
	}

	switch event := event.(type) {
	case *github.IssuesEvent:
		log.Println("INFO: Webhook is an Issues event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		if event.GetAction() == "closed" {
			log.Printf("INFO: Issue #%s was closed.", issueNumber)
			closeIssue(issueNumber)
		} else {
			parseMessage(event.Issue.GetBody(), issueNumber)
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Webhook is an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		parseMessage(event.Comment.GetBody(), issueNumber)
	case *github.PingEvent:
		log.Println("INFO: Received an Ping event.")
		var cnt int
		// Since this exporter only processes "issues" and "issue_comment" Github
		// webhook events, be sure that at least these two events are enabled for the
		// webhook.
		for _, v := range event.Hook.Events {
			if v == "issues" || v == "issue_comment" {
				cnt++
			}

		}
		if cnt != 2 {
			status = http.StatusExpectationFailed
		}
	default:
		status = http.StatusNotImplemented
	}

	resp.WriteHeader(status)
	return
}

func init() {
	flag.StringVar(&fListenAddress, "web.listen-address", ":9999",
		"Address to listen on for telemetry.")
	flag.StringVar(&fStateFilePath, "storage.state-file", "/tmp/gmx-state",
		"Filesystem path for the state file.")
	prometheus.MustRegister(metricMachine)
	prometheus.MustRegister(metricSite)
}

func main() {
	flag.Parse()
	restoreState()
	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fListenAddress, nil))
}
