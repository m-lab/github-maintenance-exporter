package handler

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/m-lab/github-maintenance-exporter/maintenancestate"
	"github.com/m-lab/go/rtx"
)

// Sample maintenance state as written to disk in JSON format.
var savedState = `
	{
		"Machines": {
			"mlab1.abc01.measurement-lab.org": ["1"],
			"mlab1.abc02.measurement-lab.org": ["8"],
			"mlab2.abc02.measurement-lab.org": ["8"],
			"mlab3.abc02.measurement-lab.org": ["8"],
			"mlab4.abc02.measurement-lab.org": ["8"],
			"mlab3.def01.measurement-lab.org": ["5"],
			"mlab1.uvw03.measurement-lab.org": ["4", "11"],
			"mlab2.uvw03.measurement-lab.org": ["4", "11"],
			"mlab3.uvw03.measurement-lab.org": ["4", "11"],
			"mlab4.uvw03.measurement-lab.org": ["4", "11"]
		},
		"Sites": {
			"abc02": ["8"],
			"uvw03": ["4", "11"]
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

func TestUpdateStateWithBadValue(t *testing.T) {
	h := New(nil, []byte{}, "").(*handler)
	h.updateState(nil, "", nil, "", -1)  // The -1 should not be a legal action.
}

func TestReceiveHook(t *testing.T) {
	state, _ := maintenancestate.New("/does/not/exist/and/thats/okay")
	githubSecret := []byte("goodsecret")

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
			name:           "issues-hook-good-request-6-mods",
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
			name:           "issues-hook-good-request-closeuvw03issue",
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
		{
			name:           "issue-comment-hook-flag-at-end-of-input",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusOK,
			payload: []byte(`
				{
					"action": "edited",
					"issue": {
						"number": 1,
						"body": "Put into maintenance /machine mlab1.abc01",
						"state": "open"
					}
				}
			`),
		},
	}

	for _, test := range tests {
		h := New(state, githubSecret, "mlab-oti")
		sig := generateSignature(test.secretKey, test.payload)
		req, err := http.NewRequest("POST", "/webhook", strings.NewReader(string(test.payload)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", test.eventType)
		req.Header.Set("X-Hub-Signature", sig)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if status := rec.Code; status != test.expectedStatus {
			t.Errorf("receiveHook(): test %s: wrong HTTP status: got %v; want %v",
				test.name, rec.Code, test.expectedStatus)
		}
	}
}

func TestCloseIssue(t *testing.T) {
	f, err := ioutil.TempFile("", "TestCloseIssue")
	rtx.Must(err, "Could not create tempfile")
	fname := f.Name()
	defer os.Remove(fname)
	rtx.Must(ioutil.WriteFile(fname, []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := maintenancestate.New(fname)
	rtx.Must(err, "Could not read from tmpfile")

	tests := []struct {
		name              string
		issue             string
		expectedMods      int
		closedMaintenance int
	}{
		{
			name:              "one-issue-per-entity-closes-maintenance",
			issue:             "8",
			expectedMods:      5,
			closedMaintenance: 5,
		},
		{
			name:              "multiple-issues-per-entity-does-not-close-maintenance",
			issue:             "4",
			expectedMods:      5,
			closedMaintenance: 0,
		},
	}

	for _, test := range tests {
		rtx.Must(s.Restore(), "Could not restore state from tempfile")
		h := handler{
			state: s,
		}

		totalEntitiesBefore := len(s.Machines) + len(s.Sites)
		mods := h.closeIssue(test.issue, s)
		totalEntitiesAfter := len(s.Machines) + len(s.Sites)
		closedMaintenance := totalEntitiesBefore - totalEntitiesAfter

		if mods != test.expectedMods {
			t.Errorf("closeIssue(): Expected %d state modifications; got %d",
				test.expectedMods, mods)
		}

		if closedMaintenance != test.closedMaintenance {
			t.Errorf("closeIssue(): Expected %d closed maintenances; got %d",
				test.closedMaintenance, closedMaintenance)
		}
	}
}

func TestParseMessage(t *testing.T) {
	f, err := ioutil.TempFile("", "TestCloseIssue")
	rtx.Must(err, "Could not create tempfile")
	fname := f.Name()
	defer os.Remove(fname)
	rtx.Must(ioutil.WriteFile(fname, []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := maintenancestate.New(fname)
	rtx.Must(err, "Could not restore state")

	tests := []struct {
		name         string
		msg          string
		project      string
		expectedMods int
	}{
		{
			name:         "add-1-machine-to-maintenance",
			msg:          `/machine mlab1.abc01 is in maintenance mode.`,
			project:      `mlab-oti`,
			expectedMods: 1,
		},
		{
			name:         "add-2-sites-to-maintenance",
			msg:          `Putting /site abc01 and /site xyz02 into maintenance mode.`,
			project:      `mlab-oti`,
			expectedMods: 10,
		},
		{
			name:         "add-1-sites-and-1-machine-to-maintenance",
			msg:          `Putting /site abc01 and /machine mlab1.xyz02 into maintenance mode.`,
			project:      `mlab-oti`,
			expectedMods: 6,
		},
		{
			name:         "remove-1-machine-and-1-site-from-maintenance",
			msg:          `Removing /machine mlab2.xyz01 del and /site uvw02 del from maintenance.`,
			project:      `mlab-oti`,
			expectedMods: 6,
		},
		{
			name:         "3-malformed-flags",
			msg:          `Add /machine and /site vw02 to maintenance. Removing /site lol del.`,
			project:      `mlab-oti`,
			expectedMods: 0,
		},
		{
			name:         "1-production-machine-1-staging-machine-flag",
			msg:          `Add /machine mlab2.ghi01 and /machine mlab4.ghi01 to maintenance.`,
			project:      `mlab-oti`,
			expectedMods: 1,
		},
		{
			name:         "1-sandbox-machine-1-staging-machine-flag",
			msg:          `Add /machine mlab3.hij0t and /machine mlab4.qrs01 to maintenance.`,
			project:      `mlab-oti`,
			expectedMods: 0,
		},
		{
			name:         "1-sandbox-machine-flag",
			msg:          `Add /machine mlab1.abc0t to maintenance.`,
			project:      `mlab-sandbox`,
			expectedMods: 1,
		},
		{
			name:         "2-staging-machine-flags",
			msg:          `Add /machine mlab4.abc03 and /machine mlab4.wxy01 to maintenance.`,
			project:      `mlab-staging`,
			expectedMods: 2,
		},
		{
			name:         "1-sandbox-site-flag",
			msg:          `Add /site nop0t to maintenance.`,
			project:      `mlab-sandbox`,
			expectedMods: 5,
		},
	}

	for _, test := range tests {
		h := handler{
			state: s,
		}
		mods := h.parseMessage(test.msg, "99", s, test.project)
		if mods != test.expectedMods {
			t.Errorf("parseMessage(): %s: expected %d state modifications; got %d",
				test.name, test.expectedMods, mods)
		}
	}
}