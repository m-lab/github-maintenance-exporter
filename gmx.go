package main

import (
	"encoding/gob"
	"flag"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/go-github/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	fListenAddress        string
	fMachineStateFilePath string
	fSiteStateFilePath    string

	githubSecret []byte

	maintenanceMachine = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_machine_state",
			Help: "State of machine.",
		},
		[]string{
			"machine",
			"issue",
		},
	)
	maintenanceSite = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_site_state",
			Help: "State of site.",
		},
		[]string{
			"site",
			"issue",
		},
	)
	machineStateMap = make(map[string]string)
	siteStateMap    = make(map[string]string)
)

func writeState(stateFilePath string, stateMap map[string]string) {
	stateFile, err := os.Create(stateFilePath)
	if err != nil {
		log.Fatalf("ERROR: Failed to create file %s for writing with error: %s", stateFilePath, err)
	} else {
		stateEncoder := gob.NewEncoder(stateFile)
		stateEncoder.Encode(stateMap)
		log.Printf("INFO: Successfully wrote state to %s.", stateFilePath)
	}
	stateFile.Close()
}

func restoreState(stateFilePath string, stateMap map[string]string) {
	stateFile, err := os.Open(stateFilePath)
	if err != nil {
		log.Printf("WARNING: Failed to open %s with error: %s", stateFilePath, err)
	} else {
		stateDecoder := gob.NewDecoder(stateFile)
		err = stateDecoder.Decode(&stateMap)
		if err != nil {
			log.Fatalf("ERROR: Failed to decode %s with error: %s", stateFilePath, err)
		}
		for k, issue := range stateMap {
			if strings.HasPrefix(k, `mlab`) {
				updateMachineState(k, issue, 1)
				log.Println("INFO: Successfully restored machineStateMap from disk.")
			} else {
				updateSiteState(k, issue, 1)
				log.Println("INFO: Successfully restored siteStateMap from disk.")
			}
		}
	}
	stateFile.Close()
}

func updateMachineState(machine string, issueNumber string, action float64) {
	// Updates the state map and Prometheus metric for the machine
	switch action {
	case 0:
		delete(machineStateMap, machine)
		maintenanceMachine.WithLabelValues(machine+".measurement-lab.org", issueNumber).Set(action)
		log.Printf("INFO: Machine %s was removed from maintenance.", machine)
	case 1:
		machineStateMap[machine] = issueNumber
		maintenanceMachine.WithLabelValues(machine+".measurement-lab.org", issueNumber).Set(action)
		log.Printf("INFO: Machine %s was added to maintenance.", machine)
	case 2:
		for machine, issue := range machineStateMap {
			if issue == issueNumber {
				delete(machineStateMap, machine)
			}
			maintenanceMachine.WithLabelValues(machine+".measurement-lab.org", issueNumber).Set(0)
			log.Printf("INFO: Machine %s was removed from maintenance because issue was closed.", machine)
		}
	default:
		log.Printf("ERROR: Unknown machine action type: %f", action)
	}
}

func updateSiteState(site string, issueNumber string, action float64) {
	// Updates the state map and Prometheus metric for a site
	switch action {
	case 0:
		delete(siteStateMap, site)
		maintenanceSite.WithLabelValues(site, issueNumber).Set(action)
		log.Printf("INFO: Site %s was removed from maintenance.", site)
	case 1:
		siteStateMap[site] = issueNumber
		maintenanceSite.WithLabelValues(site, issueNumber).Set(action)
		log.Printf("INFO: Site %s was added to maintenance.", site)
	case 2:
		for site, issue := range siteStateMap {
			if issue == issueNumber {
				delete(siteStateMap, site)
			}
			maintenanceSite.WithLabelValues(site, issueNumber).Set(0)
			log.Printf("INFO: Site %s was removed from maintenance because issue was closed.", site)
		}
	default:
		log.Printf("ERROR: Unknown site action type: %f", action)
	}
}

func parseMessage(msg string, num string) int {
	machineExp, _ := regexp.Compile(`\/machine (mlab[1-4]{1}\.[a-z]{3}[0-9c]{2})\s?(del)?`)
	siteExp, _ := regexp.Compile(`\/site ([a-z]{3}[0-9c]{2})\s?(del)?`)
	machineMatches := machineExp.FindAllStringSubmatch(msg, -1)
	if len(machineMatches) > 0 {
		for _, machine := range machineMatches {
			log.Printf("INFO: Flag found for machine: %s", machine[1])
			if machine[2] == "del" {
				log.Printf("INFO: Machine %s will be removed from maintenance.", machine[1])
				updateMachineState(machine[1], num, 0)
			} else {
				log.Printf("INFO: Machine %s will be added to maintenance.", machine[1])
				updateMachineState(machine[1], num, 1)
			}
			writeState(fMachineStateFilePath, machineStateMap)
		}

	}

	siteMatches := siteExp.FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if site[2] == "del" {
				log.Printf("INFO: Site %s will be removed from maintenance.", site[1])
				updateSiteState(site[1], num, 0)
			} else {
				log.Printf("INFO: Site %s will be added to maintenance.", site[1])
				updateSiteState(site[1], num, 1)
			}
			writeState(fSiteStateFilePath, siteStateMap)
		}
	}

	return http.StatusOK
}

func receiveHook(resp http.ResponseWriter, req *http.Request) {
	log.Println("INFO: Received a webhook.")
	var status int
	var issueNumber string

	payload, err := github.ValidatePayload(req, githubSecret)
	if err != nil {
		log.Printf("WARN: Validation of Webhook failed: %s", err)
		resp.WriteHeader(http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)

	switch event := event.(type) {
	case *github.IssuesEvent:
		log.Println("INFO: Received an Issues event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		if event.GetAction() == "closed" {
			log.Printf("INFO: Issue #%s was closed.", issueNumber)
			updateMachineState("n/a", issueNumber, 2)
			updateSiteState("n/a", issueNumber, 2)
			status = http.StatusOK
		} else {
			status = parseMessage(event.Issue.GetBody(), issueNumber)
		}
	case *github.IssueCommentEvent:
		log.Println("INFO: Received an IssueComment event.")
		issueNumber = strconv.Itoa(event.Issue.GetNumber())
		status = parseMessage(event.Comment.GetBody(), issueNumber)
	case *github.PingEvent:
		log.Println("INFO: Received an Ping event.")
		status = http.StatusOK
	default:
		status = http.StatusNotImplemented
	}

	resp.WriteHeader(status)
	return
}

func init() {
	flag.StringVar(&fListenAddress, "web.listen-address", ":9999",
		"Address to listen on for telemetry.")
	flag.StringVar(&fMachineStateFilePath, "storage.machine-state-file", "/tmp/gmx-machine-state",
		"Filesystem path for machine state file.")
	flag.StringVar(&fSiteStateFilePath, "storage.site-state-file", "/tmp/gmx-site-state",
		"Filesystem path for site state file.")
	prometheus.MustRegister(maintenanceMachine)
	prometheus.MustRegister(maintenanceSite)
	restoreState(fMachineStateFilePath, machineStateMap)
	restoreState(fSiteStateFilePath, siteStateMap)
}

func main() {
	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fListenAddress, nil))
}
