package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func generateSignature(secret, msg []byte) string {
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg)
	return "sha1=" + hex.EncodeToString(mac.Sum(nil))
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
			name:           "ping-hook-wrong-events-registered",
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
			payload:        []byte(`{"key":"value"}`),
		},
		{
			name:           "issue-comment-hook-malformed-payload",
			secretKey:      githubSecret,
			eventType:      "issue_comment",
			expectedStatus: http.StatusBadRequest,
			payload:        []byte(`{ "malformed; 'json }]]}`),
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
		handler := http.HandlerFunc(receiveHook)
		handler.ServeHTTP(rec, req)

		if status := rec.Code; status != test.expectedStatus {
			t.Errorf("receiveHook(): test %s: wrong HTTP status: got %v; want %v",
				test.name, rec.Code, test.expectedStatus)
		}
	}
}
