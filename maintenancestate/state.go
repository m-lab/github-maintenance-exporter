package maintenancestate

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"strings"

	"github.com/m-lab/github-maintenance-exporter/metrics"
	"github.com/m-lab/go/rtx"
	"github.com/prometheus/client_golang/prometheus"
)

type Action float64

const (
	EnterMaintenance Action = 1
	LeaveMaintenance Action = 0
)

// MaintenanceState is a struct for storing both machine and site maintenance states.
type MaintenanceState struct {
	Machines, Sites map[string][]string
	filename        string
}

// Looks for a string a slice.
func stringInSlice(s string, list []string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// Removes a single issue from a site/machine. If the issue was the last one
// associated with the site/machine, it will also remove the site/machine
// from maintenance.
func removeIssue(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string) int {

	var mods = 0
	mapElement := stateMap[mapKey]

	issueIndex := stringInSlice(issueNumber, mapElement)
	if issueIndex >= 0 {
		mapElement[issueIndex] = mapElement[len(mapElement)-1]
		mapElement = mapElement[:len(mapElement)-1]
		if len(mapElement) == 0 {
			delete(stateMap, mapKey)
			// If this is a machine state, then we need to pass mapKey twice, once for the
			// "machine" label and once for the "node" label.
			if strings.HasPrefix(mapKey, "mlab") {
				metricState.WithLabelValues(mapKey, mapKey).Set(0)
			} else {
				metricState.WithLabelValues(mapKey).Set(0)
			}
		} else {
			stateMap[mapKey] = mapElement
		}
		log.Printf("INFO: %s was removed from maintenance for issue #%s", mapKey, issueNumber)
		mods++
	}
	return mods
}

// updateState modifies the maintenance state of a machine or site in the
// in-memory map as well as updating the Prometheus metric.
func updateState(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action Action) {

	switch action {
	case LeaveMaintenance:
		removeIssue(stateMap, mapKey, metricState, issueNumber)
	case EnterMaintenance:
		// Don't enter maintenance more than once for a given issue.
		issueIndex := stringInSlice(issueNumber, stateMap[mapKey])
		if issueIndex >= 0 {
			log.Printf("INFO: %s is already in maintenance for issue #%s", mapKey, issueNumber)
			return
		}
		stateMap[mapKey] = append(stateMap[mapKey], issueNumber)
		// If this is a machine state, then we need to pass mapKey twice, once for the
		// "machine" label and once for the "node" label.
		if strings.HasPrefix(mapKey, "mlab") {
			metricState.WithLabelValues(mapKey, mapKey).Set(float64(action))
		} else {
			metricState.WithLabelValues(mapKey).Set(float64(action))
		}
		log.Printf("INFO: %s was added to maintenance for issue #%s", mapKey, issueNumber)
	default:
		log.Printf("WARNING: Unknown action type: %f", action)
	}
}

func (s *MaintenanceState) Restore() error {
	data, err := ioutil.ReadFile(s.filename)
	if err != nil {
		log.Printf("ERROR: Failed to read state data from %s: %s", s.filename, err)
		metrics.Error.WithLabelValues("readfile", "maintenancestate.Restore").Inc()
		return err
	}

	err = json.Unmarshal(data, &s)
	if err != nil {
		log.Printf("ERROR: Failed to unmarshal JSON: %s", err)
		metrics.Error.WithLabelValues("unmarshaljson", "maintenancestate.Restore").Inc()
		return err
	}

	// Restore machine maintenance state.
	for machine := range s.Machines {
		metrics.Machine.WithLabelValues(machine, machine).Set(float64(EnterMaintenance))
	}

	// Restore site maintenance state.
	for site := range s.Sites {
		metrics.Site.WithLabelValues(site).Set(float64(EnterMaintenance))
	}

	log.Printf("INFO: Successfully restored %s from disk.", s.filename)
	return nil
}

// Write serializes the content of a maintenanceState object into JSON and
// writes it to a file on disk.
func (s *MaintenanceState) Write() error {
	data, err := json.MarshalIndent(s, "", "    ")
	rtx.Must(err, "Could not marshal MaintenanceState to a buffer.  This should never happen.")

	err = ioutil.WriteFile(s.filename, data, 0664)
	if err != nil {
		log.Printf("ERROR: Failed to write state to %s: %s", s.filename, err)
		metrics.Error.WithLabelValues("writefile", "maintenancestate.Write").Add(1)
		return err
	}

	log.Printf("INFO: Successfully wrote state to %s.", s.filename)
	return nil
}

func (s *MaintenanceState) UpdateMachine(machine string, action Action, issue string) int {
	updateState(s.Machines, machine, metrics.Machine, issue, action)
	return 1
}

func (s *MaintenanceState) UpdateSite(site string, action Action, issue string) int {
	var mods int
	updateState(s.Sites, site, metrics.Site, issue, action)
	mods++
	// Since site is leaving/entering maintenance, remove all associated machine maintenances.
	for _, num := range []string{"1", "2", "3", "4"} {
		machine := "mlab" + num + "." + site + ".measurement-lab.org"
		mods += s.UpdateMachine(machine, action, issue)
	}
	return mods
}

// CloseIssue removes any machines and sites from maintenance mode when the
// issue that added them to maintenance mode is closed. The return value is the
// number of modifications that were made to the machine and site maintenance
// state.
func (s *MaintenanceState) CloseIssue(issue string) int {
	var totalMods = 0
	// Remove any sites from maintenance that were set by this issue.
	for site, issues := range s.Sites {
		issueIndex := stringInSlice(issue, issues)
		if issueIndex >= 0 {
			mods := removeIssue(s.Sites, site, metrics.Site, issue)
			totalMods = totalMods + mods
			// Since site is leaving maintenance, remove all associated machine maintenances.
			for _, num := range []string{"1", "2", "3", "4"} {
				machine := "mlab" + num + "." + site + ".measurement-lab.org"
				mods := removeIssue(s.Machines, machine, metrics.Machine, issue)
				totalMods = totalMods + mods
			}
		}
	}

	// Remove any machines from maintenance that were set by this issue.
	for machine, issues := range s.Machines {
		issueIndex := stringInSlice(issue, issues)
		if issueIndex >= 0 {
			mods := removeIssue(s.Machines, machine, metrics.Machine, issue)
			totalMods = totalMods + mods
		}
	}

	return totalMods
}

func New(filename string) (*MaintenanceState, error) {
	s := &MaintenanceState{
		Machines: make(map[string][]string),
		Sites:    make(map[string][]string),
		filename: filename,
	}
	err := s.Restore()
	if err != nil {
		log.Printf("WARNING: Failed to restore state file %s: %s", filename, err)
		metrics.Error.WithLabelValues("restore", "maintenancestate.New").Add(1)
	}
	return s, err
}
