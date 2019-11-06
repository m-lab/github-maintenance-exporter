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

func TestReceiveHook(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestReceiveHook")
	rtx.Must(err, "Could not make tempfile")
	defer os.RemoveAll(dir)
	githubSecret := []byte("goodsecret")

	tests := []struct {
		name           string
		secretKey      []byte
		stateFile      string
		initialState   string
		eventType      string
		expectedStatus int
		expectedState  string
		payload        string
	}{
		{
			name:           "ping-hook-missing-issues-issue_comment-events",
			secretKey:      githubSecret,
			eventType:      "ping",
			expectedStatus: http.StatusExpectationFailed,
			payload: `
					{
						"hook": {
					 		"type": "App",
					  		"id": 11,
					  		"active": true,
					  		"events": ["issues", "label", "pull_request"],
						  	"app_id":37
						}
					}
				`,
			expectedState: ``,
		},
		{
			name:           "issues-hook-bad-signature",
			secretKey:      []byte("badsecret"),
			eventType:      "issues",
			expectedStatus: http.StatusUnauthorized,
			payload:        `{"fake":"data"}`,
		},
		{
			name:           "issues-hook-unsupported-event-type",
			secretKey:      githubSecret,
			eventType:      "pull_request",
			expectedStatus: http.StatusNotImplemented,
			payload:        `{"fake":"data"}`,
		},
		{
			name:           "issues-hook-good-request-6-mods",
			secretKey:      githubSecret,
			eventType:      "issues",
			expectedStatus: http.StatusOK,
			payload: `
					{
						"action": "edited",
						"issue": {
							"number": 3,
							"body": "Put /machine mlab1.abc01 and /site xyz01 into maintenance."
						}
					}
				`,
			expectedState: `
					{
						"Machines": {
							"mlab1.abc01.measurement-lab.org": ["3"],
							"mlab1.xyz01.measurement-lab.org": ["3"],
							"mlab2.xyz01.measurement-lab.org": ["3"],
							"mlab3.xyz01.measurement-lab.org": ["3"],
							"mlab4.xyz01.measurement-lab.org": ["3"]
						},
						"Sites": {
							"xyz01": ["3"]
						}
					}
				`,
		},
		{
			name:           "issues-hook-good-request-closeuvw03issue",
			secretKey:      githubSecret,
			eventType:      "issues",
			expectedStatus: http.StatusOK,
			payload: `
					{
						"action": "closed",
						"issue": {
							"number": 3,
							"body": "Issue resolved."
						}
					}
				`,
			initialState: `
				{
					"Machines": {
						"mlab1.abc01.measurement-lab.org": ["3"],
						"mlab1.xyz01.measurement-lab.org": ["3"],
						"mlab2.xyz01.measurement-lab.org": ["3", "5"],
						"mlab3.xyz01.measurement-lab.org": ["3"],
						"mlab4.xyz01.measurement-lab.org": ["3"]
					},
					"Sites": {
						"xyz01": ["3"]
					}
				}
				`,
			expectedState: `
				{
					"Machines": {
						"mlab2.xyz01.measurement-lab.org": ["5"]
					},
					"Sites": {
					}
				}
			`,
		},
		{
			name:           "issues-hook-good-request-unsupported-action",
			secretKey:      githubSecret,
			eventType:      "issues",
			expectedStatus: http.StatusNotImplemented,
			payload: `
					{
						"action": "unlabeled",
						"issue": {
							"number": 2,
							"body": "Issue was unlabeled."
						}
					}
				`,
		},
		{
			name:           "issue-comment-hook-malformed-payload",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusBadRequest,
			payload:        `"malformed; 'json }]]}`,
		},
		{
			name:           "issue-comment-hook-on-closed-issue",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusExpectationFailed,
			payload: `
					{
						"action": "created",
						"issue": {
							"number": 19,
							"body": "Closed issue received a new comment.",
							"state": "closed"
						}
					}
				`,
		},
		{
			name:           "issue-comment-hook-good-request-1-mod",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusOK,
			payload: `
					{
						"action": "edited",
						"issue": {
							"number": 1,
							"state": "open"
						},
						"comment": {
							"body": "Take /machine mlab1.abc01 del out of maintenance."
						}
					}
				`,
			initialState: `
				{
					"Machines": {
						"mlab1.abc01.measurement-lab.org": ["1"],
						"mlab2.xyz01.measurement-lab.org": ["3", "5"]
					}
				}
				`,
			expectedState: `
				{
					"Machines": {
						"mlab2.xyz01.measurement-lab.org": ["3", "5"]
					}
				}
			`,
		},
		{
			name:           "issue-comment-hook-flag-at-end-of-input",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusOK,
			payload: `
					{
						"action": "edited",
						"issue": {
							"number": 1,
							"state": "open"
						},
						"comment": {
							"body": "Put into maintenance /machine mlab1.abc01"
						}
					}
				`,
			expectedState: `
				{
					"Machines": {
						"mlab1.abc01.measurement-lab.org": ["1"]
					}
				}
				`,
		},
		{
			name:           "bad-state-filename",
			secretKey:      githubSecret,
			stateFile:      dir + "/this/does/not/exist",
			eventType:      "issue_comment",
			expectedStatus: http.StatusInternalServerError,
			payload: `
				{
					"action": "edited",
					"issue": {
						"number": 1,
						"state": "open"
					},
					"comment": {
						"body": "Put /site xyz07 into maintenance."
					}
				}
			`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.stateFile == "" {
				test.stateFile = dir + "/" + test.name
			}
			ioutil.WriteFile(test.stateFile, []byte(test.initialState), 0644)
			state, err := maintenancestate.New(test.stateFile)
			h := New(state, githubSecret, "mlab-oti")
			sig := generateSignature(test.secretKey, []byte(test.payload))
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
			if test.expectedStatus == http.StatusOK {
				rtx.Must(ioutil.WriteFile(dir+"/expectedstate.json", []byte(test.expectedState), 0644), "Could not write golden state")
				savedState, _ := maintenancestate.New(dir + "/expectedstate.json")
				savedState.Write()
				expectedStateBytes, _ := ioutil.ReadFile(dir + "/expectedstate.json")
				test.expectedState = string(expectedStateBytes)

				actualStateBytes, _ := ioutil.ReadFile(test.stateFile)
				actualState := string(actualStateBytes)
				if test.expectedState != actualState {
					t.Errorf("State was not changed correctly: %s != %s", test.expectedState, actualState)
				}
			}
		})
	}
}

func TestParseMessage(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestCloseIssue")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)

	tests := []struct {
		name         string
		msg          string
		issue        string
		project      string
		expectedMods int
	}{
		{
			name:         "add-1-machine-to-maintenance",
			msg:          `/machine mlab1.abc01 is in maintenance mode.`,
			issue:        "99",
			project:      `mlab-oti`,
			expectedMods: 1,
		},
		{
			name:         "add-2-sites-to-maintenance",
			msg:          `Putting /site abc01 and /site xyz02 into maintenance mode.`,
			issue:        "99",
			project:      `mlab-oti`,
			expectedMods: 10,
		},
		{
			name:         "add-1-sites-and-1-machine-to-maintenance",
			msg:          `Putting /site abc01 and /machine mlab1.xyz02 into maintenance mode.`,
			issue:        "99",
			project:      `mlab-oti`,
			expectedMods: 6,
		},
		{
			name:         "remove-1-machine-and-1-site-from-maintenance",
			msg:          `Removing /machine mlab2.xyz01 del and /site uvw03 del from maintenance.`,
			issue:        "11",
			project:      `mlab-oti`,
			expectedMods: 6,
		},
		{
			name:         "3-malformed-flags",
			msg:          `Add /machine and /site vw02 to maintenance. Removing /site lol del.`,
			issue:        "99",
			project:      `mlab-oti`,
			expectedMods: 0,
		},
		{
			name:         "1-production-machine-1-staging-machine-flag",
			msg:          `Add /machine mlab2.ghi01 and /machine mlab4.ghi01 to maintenance.`,
			issue:        "99",
			project:      `mlab-oti`,
			expectedMods: 1,
		},
		{
			name:         "1-sandbox-machine-1-staging-machine-flag",
			msg:          `Add /machine mlab3.hij0t and /machine mlab4.qrs01 to maintenance.`,
			issue:        "99",
			project:      `mlab-oti`,
			expectedMods: 0,
		},
		{
			name:         "1-sandbox-machine-flag",
			msg:          `Add /machine mlab1.abc0t to maintenance.`,
			issue:        "99",
			project:      `mlab-sandbox`,
			expectedMods: 1,
		},
		{
			name:         "2-staging-machine-flags",
			msg:          `Add /machine mlab4.abc03 and /machine mlab4.wxy01 to maintenance.`,
			issue:        "99",
			project:      `mlab-staging`,
			expectedMods: 2,
		},
		{
			name:         "1-sandbox-site-flag",
			msg:          `Add /site nop0t to maintenance.`,
			issue:        "99",
			project:      `mlab-sandbox`,
			expectedMods: 5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rtx.Must(ioutil.WriteFile(dir+"/"+test.name, []byte(savedState), 0644), "Could not write state to tempfile")
			s, err := maintenancestate.New(dir + "/" + test.name)
			rtx.Must(err, "Could not restore state")
			h := handler{
				state:   s,
				project: test.project,
			}
			mods := h.parseMessage(test.msg, test.issue)
			if mods != test.expectedMods {
				h.state.Write()
				newstate, _ := ioutil.ReadFile(dir + "/" + test.name)
				t.Errorf("parseMessage(): %s (issue %s): expected %d state modifications; got %d (%s)",
					test.name, test.issue, test.expectedMods, mods, string(newstate))
			}
		})
	}
}
