package main

import (
	"log"
	"net/http"
	"regexp"
	"strconv"

	"github.com/google/go-github/github"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	listenPort      = "9999"
	githubSecret    = []byte("7f29588262f53e45ea1aa1da7e0f13b9105e6589")
	maintenanceNode = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_node_status",
			Help: "Status of node.",
		},
		[]string{
			"machine",
			"issue",
		},
	)
	maintenanceSite = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_site_status",
			Help: "Status of site.",
		},
		[]string{
			"site",
			"issue",
		},
	)
	nodeStatusMap = make(map[string]string)
	siteStatusMap = make(map[string]string)
)

func updateNodeStatus(node string, issueNumber string, action float64) {
	// Updates the status map and Prometheus metric for the node
	switch action {
	case 0:
		delete(nodeStatusMap, node)
		maintenanceNode.WithLabelValues(node+".measurement-lab.org", issueNumber).Set(action)
		log.Printf("INFO: Node %s was removed from maintenance.", node)
	case 1:
		nodeStatusMap[node] = issueNumber
		maintenanceNode.WithLabelValues(node+".measurement-lab.org", issueNumber).Set(action)
		log.Printf("INFO: Node %s was added to maintenance.", node)
	case 2:
		for node, issue := range nodeStatusMap {
			if issue == issueNumber {
				delete(nodeStatusMap, node)
			}
			maintenanceNode.WithLabelValues(node+".measurement-lab.org", issueNumber).Set(0)
			log.Printf("INFO: Node %s was removed from maintenance because issue was closed.", node)
		}
	default:
		log.Printf("ERROR: Unknown node action type: %f", action)
	}
}

func updateSiteStatus(site string, issueNumber string, action float64) {
	// Updates the status map and Prometheus metric for a site
	switch action {
	case 0:
		delete(siteStatusMap, site)
		maintenanceNode.WithLabelValues(site, issueNumber).Set(action)
		log.Printf("INFO: Site %s was removed from maintenance.", site)
	case 1:
		siteStatusMap[site] = issueNumber
		maintenanceNode.WithLabelValues(site, issueNumber).Set(action)
		log.Printf("INFO: Site %s was added to maintenance.", site)
	case 2:
		for site, issue := range siteStatusMap {
			if issue == issueNumber {
				delete(siteStatusMap, site)
			}
			maintenanceNode.WithLabelValues(site, issueNumber).Set(0)
			log.Printf("INFO: Site %s was removed from maintenance because issue was closed.", site)
		}
	default:
		log.Printf("ERROR: Unknown site action type: %f", action)
	}
}

func parseMessage(msg string, num string) int {
	nodeExp, _ := regexp.Compile(`\/node (mlab[1-4]{1}\.[a-z]{3}[0-9c]{2})\s?(del)?`)
	siteExp, _ := regexp.Compile(`\/site ([a-z]{3}[0-9c]{2})\s?(del)?`)
	nodeMatches := nodeExp.FindAllStringSubmatch(msg, -1)
	if len(nodeMatches) > 0 {
		for _, node := range nodeMatches {
			log.Printf("INFO: Flag found for node: %s", node[1])
			if node[2] == "del" {
				log.Printf("INFO: Node %s will be removed from maintenance.", node[1])
				updateNodeStatus(node[1], num, 0)
			} else {
				log.Printf("INFO: Node %s will be added to maintenance.", node[1])
				updateNodeStatus(node[1], num, 1)
			}
		}

	}

	siteMatches := siteExp.FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			log.Printf("INFO: Flag found for site: %s", site[1])
			if site[2] == "del" {
				log.Printf("INFO: Site %s will be removed from maintenance.", site[1])
				updateSiteStatus(site[1], num, 0)
			} else {
				log.Printf("INFO: Site %s will be added to maintenance.", site[1])
				updateSiteStatus(site[1], num, 1)
			}
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
			updateNodeStatus("n/a", issueNumber, 2)
			updateSiteStatus("n/a", issueNumber, 2)
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
	prometheus.MustRegister(maintenanceNode)
	prometheus.MustRegister(maintenanceSite)
}

func main() {
	http.HandleFunc("/webhook", receiveHook)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(":"+listenPort, nil))
}
