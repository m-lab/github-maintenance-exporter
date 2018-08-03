package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	//"github.com/rjz/githubhook"
)

const githubSecret = "7f29588262f53e45ea1aa1da7e0f13b9105e6589"

var nodeStatusMap = make(map[string]int)
var siteStatusMap = make(map[string]int)

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
	Body   []byte       `json:"body"`
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
	Body []byte `json:"body"`
}

func updateNodeStatusMap(node []byte) {
	// do something
}

func updateSiteStatusMap(site []byte) {
	// do something
}

func parseMessage(msg []byte) string {
	nodeExp, _ := regexp.Compile(`\/node (mlab[1-4]{1}\.[a-z]{3}[0-9c]{2})`)
	siteExp, _ := regexp.Compile(`\/site ([a-z]{3}[0-9c]{2})`)

	nodeMatches := nodeExp.FindAllSubmatch(msg, -1)
	if len(nodeMatches) > 0 {
		for _, node := range nodeMatches {
			updateNodeStatusMap(node)
		}

	}

	siteMatches := siteExp.FindAllSubmatch(msg, -1)
	if len(siteMatches) > 0 {
		for _, site := range siteMatches {
			updateSiteStatusMap(site)
		}
	}
}

func handleIssuesHook(body []byte, resp http.ResponseWriter) int {
	var issuesHook IssuesHook
	json.Unmarshal(body, &issuesHook)
	fmt.Printf("%+v\n", issuesHook)
	parseMessage(issuesHook.Issue.Body)
	return http.StatusOK
}

func handleIssueCommentHook(body []byte, resp http.ResponseWriter) int {
	var issueCommentHook IssueCommentHook
	json.Unmarshal(body, &issueCommentHook)
	fmt.Printf("%+v\n", issueCommentHook)
	parseMessage(issueCommentHook.Comment.Body)
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
	default:
		resp.WriteHeader(http.StatusNotImplemented)
		return
	}

	resp.WriteHeader(status)
	return
}

func main() {
	http.HandleFunc("/webhook", receiveHook)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
