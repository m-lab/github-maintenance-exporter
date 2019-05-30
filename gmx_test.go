package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Sample maintenance state as written to disk in JSON format.
var savedState = `
	{
		"Machines": {
			"mlab1.abc01.measurement-lab.org": ["1"],
			"mlab2.xyz01.measurement-lab.org": ["2"],
			"mlab3.def01.measurement-lab.org": ["8"]
		},
		"Sites": {
			"abc02": ["8"],
			"def02": ["8"],
			"uvw03": ["4"],
			"xyz03": ["5"]

		}
	}
`

// Every Github webhook contains a header field named X-Hub-Signature which
// contains a hash of the POST body using a predefined secret. This function
// generates that hash for testing.
func generateSignature(secret, msg []byte) string {
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg)
	return "sha1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestRootHandler(t *testing.T) {
	expectedStatus := http.StatusOK
	expectedPayload := "GitHub Maintenance Exporter"

	req, err := http.NewRequest("POST", "/", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	rootHandler(rec, req)

	if rec.Code != expectedStatus {
		t.Errorf("rootHandler(): test %s: wrong HTTP status: got %v; want %v",
			"TestRootHandler", rec.Code, expectedStatus)
	}

	bytes, _ := ioutil.ReadAll(rec.Body)
	payload := string(bytes)
	if string(payload) != expectedPayload {
		t.Errorf("rootHandler(): test %s: unexpected return text: got %s; want %s",
			"TestRootHandler", payload, expectedPayload)
	}
}

func TestRestoreState(t *testing.T) {
	expectedMachines := 3
	expectedSites := 4

	r := strings.NewReader(savedState)
	var s maintenanceState
	restoreState(r, &s)

	if len(s.Machines) != expectedMachines {
		t.Errorf("restoreState(): Expected %d restored machines; have %d.",
			expectedMachines, len(s.Machines))
	}

	if len(s.Sites) != expectedSites {
		t.Errorf("restoreState(): Expected %d restored sites; have %d.",
			expectedSites, len(s.Sites))
	}
}

func TestReceiveHook(t *testing.T) {
	githubSecret = []byte("goodsecret")

	tests := []struct {
		name           string
		secretKey      []byte
		eventType      string
		expectedStatus int
		payload        []byte
	}{
		{
			name:           "ping-hook-missing-issues-issue_comment-events",
			secretKey:      githubSecret,
			eventType:      "ping",
			expectedStatus: http.StatusExpectationFailed,
			payload: []byte(`
				{
					"hook": {
				 		"type": "App",
				  		"id": 11,
				  		"active": true,
				  		"events": ["issues", "label", "pull_request"],
					  	"app_id":37
					}
				}
			`),
		},
		{
			name:           "issues-hook-bad-signature",
			secretKey:      []byte("badsecret"),
			eventType:      "issues",
			expectedStatus: http.StatusUnauthorized,
			payload:        []byte(`{"fake":"data"}`),
		},
		{
			name:           "issues-hook-unsupported-event-type",
			secretKey:      githubSecret,
			eventType:      "pull_request",
			expectedStatus: http.StatusNotImplemented,
			payload:        []byte(`{"fake":"data"}`),
		},
		{
			name:           "issues-hook-good-request-2-mods",
			secretKey:      githubSecret,
			eventType:      "issues",
			expectedStatus: http.StatusOK,
			payload: []byte(`
				{
					"action": "edited",
					"issue": {
						"number": 3,
						"body": "Put /machine mlab1.abc01 and /site xyz01 into maintenance."
					}
				}
			`),
		},
		{
			name:           "issues-hook-good-request-close-issue",
			secretKey:      githubSecret,
			eventType:      "issues",
			expectedStatus: http.StatusOK,
			payload: []byte(`
				{
					"action": "closed",
					"issue": {
						"number": 3,
						"body": "Issue resolved."
					}
				}
			`),
		},
		{
			name:           "issues-hook-good-request-unsupported-action",
			secretKey:      githubSecret,
			eventType:      "issues",
			expectedStatus: http.StatusNotImplemented,
			payload: []byte(`
				{
					"action": "unlabeled",
					"issue": {
						"number": 2,
						"body": "Issue was unlabeled."
					}
				}
			`),
		},
		{
			name:           "issue-comment-hook-malformed-payload",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusBadRequest,
			payload:        []byte(`"malformed; 'json }]]}`),
		},
		{
			name:           "issue-comment-hook-on-closed-issue",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusExpectationFailed,
			payload: []byte(`
				{
					"action": "created",
					"issue": {
						"number": 19,
						"body": "Closed issue received a new comment.",
						"state": "closed"
					}
				}
			`),
		},
		{
			name:           "issue-comment-hook-good-request-1-mod",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusOK,
			payload: []byte(`
				{
					"action": "edited",
					"issue": {
						"number": 1,
						"body": "Take /machine mlab1.abc01 del out of maintenance.",
						"state": "open"
					}
				}
			`),
		},
	}

	for _, test := range tests {
		sig := generateSignature(test.secretKey, test.payload)
		req, err := http.NewRequest("POST", "/webhook", strings.NewReader(string(test.payload)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", test.eventType)
		req.Header.Set("X-Hub-Signature", sig)

		rec := httptest.NewRecorder()
		receiveHook(rec, req)

		if status := rec.Code; status != test.expectedStatus {
			t.Errorf("receiveHook(): test %s: wrong HTTP status: got %v; want %v",
				test.name, rec.Code, test.expectedStatus)
		}
	}
}

func TestCloseIssue(t *testing.T) {
	expectedMods := 3

	r := strings.NewReader(savedState)
	var s maintenanceState
	restoreState(r, &s)

	mods := closeIssue("8", &s)

	if mods != expectedMods {
		t.Errorf("closeIssue(): Expected %d state modifications; got %d", expectedMods, mods)
	}
}

func TestParseMessage(t *testing.T) {
	r := strings.NewReader(savedState)
	var s maintenanceState
	restoreState(r, &s)

	tests := []struct {
		name         string
		msg          string
		expectedMods int
	}{
		{
			name:         "add-1-machine-to-maintenance",
			msg:          `/machine mlab1.abc01 is in maintenance mode.`,
			expectedMods: 1,
		},
		{
			name:         "add-2-sites-to-maintenance",
			msg:          `Putting /site abc01 and /site xyz02 into maintenance mode.`,
			expectedMods: 2,
		},
		{
			name:         "add-1-sites-and-1-machine-to-maintenance",
			msg:          `Putting /site abc01 and /machine mlab1.xyz02 into maintenance mode.`,
			expectedMods: 2,
		},
		{
			name:         "remove-1-machine-and-1-site-from-maintenance",
			msg:          `Removing /machine mlab2.xyz01 del and /site uvw02 del from maintenance.`,
			expectedMods: 2,
		},
		{
			name:         "3-malformed-flags",
			msg:          `Add /machine and /site vw02 to maintenance. Removing /site lol del.`,
			expectedMods: 0,
		},
	}

	for _, test := range tests {
		mods := parseMessage(test.msg, "99", &s)
		if mods != test.expectedMods {
			t.Errorf("parseMessage(): %s: expected %d state modifications; got %d",
				test.name, test.expectedMods, mods)
		}
	}
}
