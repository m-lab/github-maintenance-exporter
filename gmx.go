package main

import (
	"encoding/gob"
	"flag"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/google/go-github/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	fListenAddress        string
	fMachineStateFilePath string
	fSiteStateFilePath    string

	githubSecret = []byte("7f29588262f53e45ea1aa1da7e0f13b9105e6589")

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

func writeMachineState() {
	machineStateFile, err := os.Create(fMachineStateFilePath)
	if err == nil {
		machineStateFile, err := os.Create(fMachineStateFilePath)
		if err != nil {
			log.Fatalf("ERROR: Failed to write file %s with error: %s", fMachineStateFilePath, err)
		}
		machineStateEncoder := gob.NewEncoder(machineStateFile)
		machineStateEncoder.Encode(machineStateMap)
		log.Println("INFO: Successfully wrote machineStateMap to disk.")
	} else {
		log.Fatalf("ERROR: Failed to write file %s with error: %s", fMachineStateFilePath, err)
	}
	machineStateFile.Close()
}

func writeSiteState() {
	siteStateFile, err := os.Create(fSiteStateFilePath)
	if err == nil {
		siteStateFile, err := os.Create(fSiteStateFilePath)
		if err != nil {
			log.Fatalf("ERROR: Failed to write file %s with error: %s", fSiteStateFilePath, err)
		}
		siteStateEncoder := gob.NewEncoder(siteStateFile)
		siteStateEncoder.Encode(siteStateMap)
		log.Println("INFO: Successfully wrote siteStateMap to disk.")
	} else {
		log.Fatalf("ERROR: Failed to write file %s with error: %s", fMachineStateFilePath, err)
	}
	siteStateFile.Close()
}

func restoreState() {
	machineStateFile, err := os.Open(fMachineStateFilePath)
	if err == nil {
		machineStateDecoder := gob.NewDecoder(machineStateFile)
		err = machineStateDecoder.Decode(&machineStateMap)
		if err != nil {
			log.Fatalf("ERROR: Failed to decode %s with error: %s", fMachineStateFilePath, err)
		}
		for machine, issue := range machineStateMap {
			updateMachineState(machine, issue, 1)
		}
		log.Println("INFO: Successfully restored machineStateMap from disk.")
	} else {
		log.Printf("WARNING: Failed to open %s with error: %s", fMachineStateFilePath, err)
	}
	machineStateFile.Close()

	siteStateFile, err := os.Open(fSiteStateFilePath)
	if err == nil {
		siteStateDecoder := gob.NewDecoder(siteStateFile)
		err = siteStateDecoder.Decode(&siteStateMap)
		if err != nil {
			log.Fatalf("ERROR: Failed to decode %s with error: %s", fSiteStateFilePath, err)
		}
		for site, issue := range siteStateMap {
			updateSiteState(site, issue, 1)
		}
		log.Println("INFO: Successfully restored siteStateMap from disk.")
	} else {
		log.Printf("WARNING: Failed to open %s with error: %s", fSiteStateFilePath, err)
	}
	siteStateFile.Close()
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
			writeMachineState()
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
			writeSiteState()
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
	restoreState()
}

func main() {
	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fListenAddress, nil))
}
