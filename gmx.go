// A simple Prometheus exporter that receives Github webhooks for issues and
// issue_comments events and parses the issue or comment body for special flags
// that indicate that a machine or site should be in maintenance mode.
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
	"time"

	"github.com/m-lab/github-maintenance-exporter/handler"
	"github.com/m-lab/github-maintenance-exporter/maintenancestate"
	"github.com/m-lab/github-maintenance-exporter/sites"
	"github.com/m-lab/go/memoryless"
	"github.com/m-lab/go/rtx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	fListenAddress    = flag.String("web.listen-address", ":9999", "Address to listen on for telemetry.")
	fStateFilePath    = flag.String("storage.state-file", "/tmp/gmx-state", "Filesystem path for the state file.")
	fGitHubSecretPath = flag.String("storage.github-secret", "", "Filesystem path of file containing the shared Github webhook secret.")
	fProject          = flag.String("project", "", "GCP project where this instance is running.")
	fReloadMin        = flag.Duration("reloadmin", time.Hour, "Minimum time to wait between reloads of backing data")
	fReloadTime       = flag.Duration("reloadtime", 5*time.Hour, "Expected time to wait between reloads of backing data")
	fReloadMax        = flag.Duration("reloadmax", 24*time.Hour, "Maximum time to wait between reloads of backing data")

	// Variables to aid in the testing of main()
	mainCtx, mainCancel = context.WithCancel(context.Background())
	validProjects       = []string{"mlab-sandbox", "mlab-staging", "mlab-oti"}
	logFatal            = log.Fatal
)

// rootHandler implements the simplest possible handler for root requests,
// simply printing the name of the utility and returning a 200 status. This
// could be used by, for example, kubernetes aliveness checks.
func rootHandler(resp http.ResponseWriter, req *http.Request) {
	resp.WriteHeader(http.StatusOK)
	fmt.Fprintf(resp, "GitHub Maintenance Exporter")
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

	// Exit if an invalid/unknown project is passed.
	var isValidProject = false
	for _, project := range validProjects {
		if project == *fProject {
			isValidProject = true
			break
		}
	}
	if !isValidProject {
		logFatal("Unknown project: ", *fProject)
	}

	// Create a new sites.CachingClient, and load data from the siteinfo API
	// for the first time. An error on the initial load of the siteinfo data is
	// fatal.
	sites := sites.New(*fProject)
	rtx.Must(sites.Reload(mainCtx), "could not load siteinfo data")

	// Read state and secrets off the disk.
	state, err := maintenancestate.New(*fStateFilePath, sites, *fProject)
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

	// Reload the siteinfo data periodically.
	go func() {
		reloadConfig := memoryless.Config{
			Min:      *fReloadMin,
			Max:      *fReloadMax,
			Expected: *fReloadTime,
		}
		tick, err := memoryless.NewTicker(mainCtx, reloadConfig)
		rtx.Must(err, "could not create ticker for reloading siteinfo")
		for range tick.C {
			err = sites.Reload(mainCtx)
			if err != nil {
				log.Printf("Failed to reload the siteinfo data: %v", err)
			}
		}
		state.Prune(*fProject)
	}()

	// When the context is canceled, stop serving.
	go func() {
		<-mainCtx.Done()
		srv.Close()
	}()

	// Listen forever, or until the context is closed.
	logFatal(srv.ListenAndServe())
}
