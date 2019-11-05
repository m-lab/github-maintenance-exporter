package main

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
