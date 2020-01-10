package main

import (
	"context"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/m-lab/go/osx"

	"github.com/m-lab/go/rtx"
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

func TestGithubSecretFromFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestGithubSecretFromFile")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/secret", []byte("test"), 0644), "Could not create test secret")
	b := MustReadGithubSecret(dir + "/secret")
	if !reflect.DeepEqual(b, []byte("test")) {
		t.Errorf("%v != %v", b, "test")
	}
}

func TestGithubSecretFromEnv(t *testing.T) {
	revert := osx.MustSetenv("GITHUB_WEBHOOK_SECRET", "test")
	defer revert()
	b := MustReadGithubSecret("")
	if !reflect.DeepEqual(b, []byte("test")) {
		t.Errorf("%v != %v", b, "test")
	}
}

func TestGithubSecretFromEmptyFile(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestGithubSecretFromEmptyFile")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/secret", []byte{}, 0644), "Could not create test secret")

	logFatal = func(...interface{}) { panic("testerror") }
	defer func() {
		r := recover()
		if r == nil {
			t.Error("Should have had a panic but did not")
		}
	}()

	MustReadGithubSecret(dir + "/secret")
}

func TestMainBadProject(t *testing.T) {
	logFatal = func(...interface{}) { panic("testerror") }
	defer func() {
		r := recover()
		if r == nil {
			t.Error("Should have had a panic but did not")
		}
	}()

	*fProject = "mlab-doesnotexist"

	main()
}

func TestMainViaSmokeTest(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestMainViaSmokeTest")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/secret", []byte("test"), 0644), "Could not create test secret")

	logFatal = func(...interface{}) { panic("testerror") }
	defer func() {
		r := recover()
		if r == nil {
			t.Error("Should have had a panic but did not")
		}
	}()

	*fGitHubSecretPath = dir + "/secret"
	*fStateFilePath = dir + "/state.json"
	*fListenAddress = ":0"
	*fProject = "mlab-sandbox"
	mainCtx, mainCancel = context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond)
		mainCancel()
	}()

	main() // No crash and no freeze and full coverage of main() == success
}
