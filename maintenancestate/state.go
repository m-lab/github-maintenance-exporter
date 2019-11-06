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

// Action describes what the maintenance exporter can do to a site or machine.
type Action int

const (
	// EnterMaintenance puts a machine or site into maintenance mode.
	EnterMaintenance Action = 2
	// LeaveMaintenance takes a machine or site out of maintenance mode.
	LeaveMaintenance Action = 1
)

func (a Action) StatusValue() float64 {
	return float64(int(a) - 1)
}

// This is the state that is serialized to disk.
type state struct {
	Machines, Sites map[string][]string
}

// MaintenanceState is a struct for storing both machine and site maintenance states.
type MaintenanceState struct {
	state    state
	filename string
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
	issueNumber string, action Action) int {
	switch action {
	case LeaveMaintenance:
		return removeIssue(stateMap, mapKey, metricState, issueNumber)
	case EnterMaintenance:
		// Don't enter maintenance more than once for a given issue.
		issueIndex := stringInSlice(issueNumber, stateMap[mapKey])
		if issueIndex >= 0 {
			log.Printf("INFO: %s is already in maintenance for issue #%s", mapKey, issueNumber)
			return 0
		}
		stateMap[mapKey] = append(stateMap[mapKey], issueNumber)
		// If this is a machine state, then we need to pass mapKey twice, once for the
		// "machine" label and once for the "node" label.
		if strings.HasPrefix(mapKey, "mlab") {
			metricState.WithLabelValues(mapKey, mapKey).Set(action.StatusValue())
		} else {
			metricState.WithLabelValues(mapKey).Set(action.StatusValue())
		}
		log.Printf("INFO: %s was added to maintenance for issue #%s", mapKey, issueNumber)
		return 1
	default:
		log.Printf("WARNING: Unknown action type: %d", action)
		return 0
	}
}

// Restore the maintenance state from disk.
func (ms *MaintenanceState) Restore() error {
	data, err := ioutil.ReadFile(ms.filename)
	if err != nil {
		log.Printf("ERROR: Failed to read state data from %s: %s", ms.filename, err)
		metrics.Error.WithLabelValues("readfile", "maintenancestate.Restore").Inc()
		return err
	}

	err = json.Unmarshal(data, &ms.state)
	if err != nil {
		log.Printf("ERROR: Failed to unmarshal JSON: %s", err)
		metrics.Error.WithLabelValues("unmarshaljson", "maintenancestate.Restore").Inc()
		return err
	}

	// Restore machine maintenance state.
	for machine := range ms.state.Machines {
		metrics.Machine.WithLabelValues(machine, machine).Set(EnterMaintenance.StatusValue())
	}

	// Restore site maintenance state.
	for site := range ms.state.Sites {
		metrics.Site.WithLabelValues(site).Set(EnterMaintenance.StatusValue())
	}

	log.Printf("INFO: Successfully restored %s from disk.", ms.filename)
	return nil
}

// Write serializes the content of a maintenanceState object into JSON and
// writes it to a file on disk.
func (ms *MaintenanceState) Write() error {
	data, err := json.MarshalIndent(ms.state, "", "    ")
	rtx.Must(err, "Could not marshal MaintenanceState to a buffer.  This should never happen.")

	err = ioutil.WriteFile(ms.filename, data, 0664)
	if err != nil {
		log.Printf("ERROR: Failed to write state to %s: %s", ms.filename, err)
		metrics.Error.WithLabelValues("writefile", "maintenancestate.Write").Add(1)
		return err
	}

	log.Printf("INFO: Successfully wrote state to %s.", ms.filename)
	return nil
}

// UpdateMachine causes a single machine to enter or exit maintenance mode.
func (ms *MaintenanceState) UpdateMachine(machine string, action Action, issue string) int {
	return updateState(ms.state.Machines, machine, metrics.Machine, issue, action)
}

// UpdateSite causes a whole site to enter or exit maintenance mode.
func (ms *MaintenanceState) UpdateSite(site string, action Action, issue string) int {
	mods := updateState(ms.state.Sites, site, metrics.Site, issue, action)
	// Since site is leaving/entering maintenance, remove all associated machine maintenances.
	for _, num := range []string{"1", "2", "3", "4"} {
		machine := "mlab" + num + "." + site + ".measurement-lab.org"
		mods += ms.UpdateMachine(machine, action, issue)
	}
	log.Println("Mods is", mods)
	return mods
}

// CloseIssue removes any machines and sites from maintenance mode when the
// issue that added them to maintenance mode is closed. The return value is the
// number of modifications that were made to the machine and site maintenance
// state.
func (ms *MaintenanceState) CloseIssue(issue string) int {
	var totalMods = 0
	// Remove any sites from maintenance that were set by this issue.
	for site := range ms.state.Sites {
		totalMods += ms.UpdateSite(site, LeaveMaintenance, issue)
	}

	// Remove any machines from maintenance that were set by this issue.
	for machine := range ms.state.Machines {
		totalMods += ms.UpdateMachine(machine, LeaveMaintenance, issue)
	}

	return totalMods
}

// New creates a MaintenanceState based on the passed-in filename. If it can't
// be restored from disk, it also generates an error.
func New(filename string) (*MaintenanceState, error) {
	s := &MaintenanceState{
		state: state{
			Machines: make(map[string][]string),
			Sites:    make(map[string][]string),
		},
		filename: filename,
	}
	err := s.Restore()
	if err != nil {
		log.Printf("WARNING: Failed to restore state file %s: %s", filename, err)
		metrics.Error.WithLabelValues("restore", "maintenancestate.New").Add(1)
	}
	return s, err
}
