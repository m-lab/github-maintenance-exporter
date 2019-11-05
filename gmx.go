// Copyright 2018 github-maintenance-exporter Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//////////////////////////////////////////////////////////////////////////////

// A simple Prometheus exporter that receives Github webhooks for issues and
// issue_comments events and parses the issue or comment body for special flags
// that indicate that a machine or site should be in maintenace mode.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/m-lab/github-maintenance-exporter/handler"
	"github.com/m-lab/github-maintenance-exporter/maintenancestate"
	"github.com/m-lab/go/rtx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const cEnterMaintenance float64 = 1
const cLeaveMaintenance float64 = 0

var (
	fListenAddress    = flag.String("web.listen-address", ":9999", "Address to listen on for telemetry.")
	fStateFilePath    = flag.String("storage.state-file", "/tmp/gmx-state", "Filesystem path for the state file.")
	fGitHubSecretPath = flag.String("storage.github-secret", "", "Filesystem path of file containing the shared Github webhook secret.")
	fProject          = flag.String("project", "mlab-oti", "GCP project where this instance is running.")

	// Variables to aid in the testing of main()
	mainCtx, mainCancel = context.WithCancel(context.Background())
	logFatal            = log.Fatal
)

// rootHandler implements the simplest possible handler for root requests,
// simply printing the name of the utility and returning a 200 status. This
// could be used by, for example, kubernetes aliveness checks.
func rootHandler(resp http.ResponseWriter, req *http.Request) {
	resp.WriteHeader(http.StatusOK)
	fmt.Fprintf(resp, "GitHub Maintenance Exporter")
	return
}

// MustReadGithubSecret reads the GitHub shared webhook secret from a file (if a
// filename is provided) or retrieves it from the environment. It exits with a
// fatal error if the secret is not found or is bad for any reason.
func MustReadGithubSecret(filename string) []byte {
	var secret []byte

	// Read it from a file or the environment.
	if filename != "" {
		var err error
		secret, err = ioutil.ReadFile(filename)
		rtx.Must(err, "ERROR: Could not read file %s", filename)
	} else {
		secret = []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
	}

	secretTrimmed := bytes.TrimSpace(secret)
	if len(secretTrimmed) == 0 {
		logFatal("ERROR: Github secret is empty.")
	}
	return secretTrimmed
}

func main() {
	defer mainCancel()
	flag.Parse()

	// Read state and secrets off the disk.
	state, err := maintenancestate.New(*fStateFilePath)
	if err != nil {
		// TODO: Should this be a fatal error, or is this okay?
		log.Printf("WARNING: Failed to open state file %s: %s", *fStateFilePath, err)
	}

	githubSecret := MustReadGithubSecret(*fGitHubSecretPath)

	// Add handlers to the default handler.
	http.HandleFunc("/", rootHandler)
	http.Handle("/webhook", handler.New(state, githubSecret, *fProject))
	http.Handle("/metrics", promhttp.Handler())

	// Set up the server
	srv := http.Server{
		Addr:    *fListenAddress,
		Handler: http.DefaultServeMux,
	}

	// When the context is canceled, stop serving.
	go func() {
		<-mainCtx.Done()
		srv.Close()
	}()

	// Listen forever, or until the context is closed.
	logFatal(srv.ListenAndServe())
}
