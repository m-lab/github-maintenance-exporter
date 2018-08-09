package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const githubSecret = "7f29588262f53e45ea1aa1da7e0f13b9105e6589"

var (
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

type IssuesHook struct {
	Action string `json:"action"`
	Issue  Issue  `json:"issue"`
}

type IssueCommentHook struct {
	Action  string  `json:action`
	Issue   Issue   `json:"issue"`
	Comment Comment `json:"comment"`
}

type Issue struct {
	Body   string       `json:"body"`
	Labels []IssueLabel `json:"labels"`
	Number int          `json:"number"`
	State  string       `json:"state"`
	Title  string       `json:"title"`
	URL    string       `json:"url"`
}

type IssueLabel struct {
	Name string `json:"name"`
}

type Comment struct {
	Body string `json:"body"`
}

func updateNodeStatusMap(node string, issueNumber string, action float64) {
	if action == 0 {
		delete(nodeStatusMap, node)
	} else {
		nodeStatusMap[node] = issueNumber
	}
	maintenanceNode.WithLabelValues(node+".measurement-lab.org", issueNumber).Set(action)
	log.Printf("%+v", nodeStatusMap)
}

func updateSiteStatusMap(site string, issueNumber string, action float64) {
	if action == 0 {
		delete(siteStatusMap, site)
	} else {
		siteStatusMap[site] = issueNumber
	}
	maintenanceSite.WithLabelValues(site, issueNumber).Set(action)
	log.Printf("%+v", siteStatusMap)
}

func parseMessage(msg string, num string) {
	nodeExp, _ := regexp.Compile(`\/node (mlab[1-4]{1}\.[a-z]{3}[0-9c]{2})\s?(del)?`)
	siteExp, _ := regexp.Compile(`\/site ([a-z]{3}[0-9c]{2})\s?(del)?`)
	nodeMatches := nodeExp.FindAllStringSubmatch(msg, -1)
	if len(nodeMatches) > 0 {
		for _, node := range nodeMatches {
			if node[2] == "del" {
				updateNodeStatusMap(node[1], num, 0)
			} else {
				updateNodeStatusMap(node[1], num, 1)
			}
		}

	}

	siteMatches := siteExp.FindAllStringSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			if site[2] == "del" {
				updateSiteStatusMap(site[1], num, 0)
			} else {
				updateSiteStatusMap(site[1], num, 1)
			}
		}
	}
}

func handleIssuesHook(body []byte, resp http.ResponseWriter) int {
	var issuesHook IssuesHook
	json.Unmarshal(body, &issuesHook)
	fmt.Printf("%+v\n", issuesHook)
	parseMessage(issuesHook.Issue.Body, fmt.Sprintf("%d", issuesHook.Issue.Number))
	return http.StatusOK
}

func handleIssueCommentHook(body []byte, resp http.ResponseWriter) int {
	var issueCommentHook IssueCommentHook
	json.Unmarshal(body, &issueCommentHook)
	fmt.Printf("%+v\n", issueCommentHook)
	parseMessage(issueCommentHook.Comment.Body, fmt.Sprintf("%d", issueCommentHook.Issue.Number))
	return http.StatusOK
}

func receiveHook(resp http.ResponseWriter, req *http.Request) {
	//hook, err := githubhook.Parse([]byte(githubSecret), req)
	//if err != nil {
	//	log.Fatal(err)
	//}

	eventType := req.Header.Get("X-GitHub-Event")
	body, _ := ioutil.ReadAll(req.Body)
	var status int

	switch eventType {
	case "issues":
		status = handleIssuesHook(body, resp)
	case "issue_comment":
		status = handleIssueCommentHook(body, resp)
	case "ping":
		resp.WriteHeader(http.StatusOK)
		return
	default:
		resp.WriteHeader(http.StatusNotImplemented)
		return
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
	log.Fatal(http.ListenAndServe(":9999", nil))
}
