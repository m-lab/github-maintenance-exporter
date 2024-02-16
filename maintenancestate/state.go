// Package maintenancestate provides all the structures and methods necessary to
// keep track of what is and isn't in maintenance mode, and to save and load
// that information from disk.
package maintenancestate

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/m-lab/github-maintenance-exporter/metrics"
	"github.com/m-lab/go/host"
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

// StatusValue converts the int underlying the Action into a float64 suitable
// for assigning to a gauge metric. When a site or machine is in maintenance
// mode, the value assigned to the gauge is 1, and when it is not, the value is
// 0.
func (a Action) StatusValue() float64 {
	return float64(int(a) - 1)
}

// Sites defines a new interface for interacting with the sites package.
type Sites interface {
	Reload(ctx context.Context) error
	Machines(site string) ([]string, error)
}

// This is the state that is serialized to disk.
type state struct {
	Machines, Sites map[string][]string
}

// MaintenanceState is a struct for storing both machine and site maintenance states.
type MaintenanceState struct {
	mu       sync.Mutex
	state    state
	filename string
	sites    Sites
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
	issueNumber string, project string) int {

	var mods = 0
	mapElement := stateMap[mapKey]

	issueIndex := stringInSlice(issueNumber, mapElement)
	if issueIndex >= 0 {
		mapElement[issueIndex] = mapElement[len(mapElement)-1]
		mapElement = mapElement[:len(mapElement)-1]
		if len(mapElement) == 0 {
			delete(stateMap, mapKey)
			updateMetrics(mapKey, project, LeaveMaintenance, metricState)
		} else {
			stateMap[mapKey] = mapElement
		}
		log.Printf("INFO: %s was removed from maintenance for issue #%s", mapKey, issueNumber)
		mods++
	}
	return mods
}

// updateMetrics updates the Prometheus metrics for machine or site.
func updateMetrics(mapKey string, project string, action Action, metricState *prometheus.GaugeVec) {
	// If this is a machine state, then we need to pass mapKey twice, once for the
	// "machine" label and once for the "node" label.
	if strings.HasPrefix(mapKey, "mlab") {
		// Construct and add labels for the machine.
		machineLabel := strings.Replace(mapKey, ".", "-", 1) + "." + project + ".measurement-lab.org"
		// Pick the site name from the full machine name, and use it as the
		// value of the "site" label for the metric.
		name, err := host.Parse(machineLabel)
		rtx.Must(err, "Failed to parse hostname: %s", machineLabel)
		metricState.WithLabelValues(machineLabel, machineLabel, name.Site).Set(action.StatusValue())
	} else {
		metricState.WithLabelValues(mapKey).Set(action.StatusValue())
	}
}

// updateState modifies the maintenance state of a machine or site in the
// in-memory map as well as updating the Prometheus metric.
func (ms *MaintenanceState) updateState(stateMap map[string][]string, mapKey string, metricState *prometheus.GaugeVec,
	issueNumber string, action Action, project string) int {

	ms.mu.Lock()
	defer ms.mu.Unlock()

	switch action {
	case LeaveMaintenance:
		return removeIssue(stateMap, mapKey, metricState, issueNumber, project)
	case EnterMaintenance:
		// Don't enter maintenance more than once for a given issue.
		issueIndex := stringInSlice(issueNumber, stateMap[mapKey])
		if issueIndex >= 0 {
			log.Printf("INFO: %s is already in maintenance for issue #%s", mapKey, issueNumber)
			return 0
		}
		stateMap[mapKey] = append(stateMap[mapKey], issueNumber)
		updateMetrics(mapKey, project, action, metricState)
		log.Printf("INFO: %s was added to maintenance for issue #%s", mapKey, issueNumber)
		return 1
	default:
		log.Printf("WARNING: Unknown action type: %d", action)
		return 0
	}
}

// Restore the maintenance state from disk.
func (ms *MaintenanceState) Restore(project string) error {
	data, err := os.ReadFile(ms.filename)
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
		updateMetrics(machine, project, EnterMaintenance, metrics.Machine)
	}

	// Restore site maintenance state.
	for site := range ms.state.Sites {
		updateMetrics(site, project, EnterMaintenance, metrics.Site)
	}

	log.Printf("INFO: Successfully restored %s from disk.", ms.filename)
	return nil
}

// Write serializes the content of a maintenanceState object into JSON and
// writes it to a file on disk.
func (ms *MaintenanceState) Write() error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	data, err := json.MarshalIndent(ms.state, "", "    ")
	rtx.Must(err, "Could not marshal MaintenanceState to a buffer.  This should never happen.")

	err = os.WriteFile(ms.filename, data, 0664)
	if err != nil {
		log.Printf("ERROR: Failed to write state to %s: %s", ms.filename, err)
		metrics.Error.WithLabelValues("writefile", "maintenancestate.Write").Add(1)
		return err
	}

	log.Printf("INFO: Successfully wrote state to %s.", ms.filename)
	return nil
}

// UpdateMachine causes a single machine to enter or exit maintenance mode.
func (ms *MaintenanceState) UpdateMachine(machine string, action Action, issue string, project string) int {
	return ms.updateState(ms.state.Machines, machine, metrics.Machine, issue, action, project)
}

// UpdateSite causes a whole site to enter or exit maintenance mode.
func (ms *MaintenanceState) UpdateSite(site string, action Action, issue string, project string) int {
	// Enforce that the site actually exists.
	machines, err := ms.sites.Machines(site)
	if err != nil {
		log.Printf("ERROR: could not update site %s: %v", site, err)
		return 0
	}
	mods := ms.updateState(ms.state.Sites, site, metrics.Site, issue, action, project)
	// If a site is entering or leaving maintenance, automatically add/remove
	// the site's machines to/from maintenance.
	for _, m := range machines {
		machine := m + "-" + site
		mods += ms.UpdateMachine(machine, action, issue, project)
	}
	log.Println("Mods is", mods)
	return mods
}

// CloseIssue removes any machines and sites from maintenance mode when the
// issue that added them to maintenance mode is closed. The return value is the
// number of modifications that were made to the machine and site maintenance
// state.
func (ms *MaintenanceState) CloseIssue(issue string, project string) int {
	var totalMods = 0
	// Remove any sites from maintenance that were set by this issue.
	for site := range ms.state.Sites {
		totalMods += ms.UpdateSite(site, LeaveMaintenance, issue, project)
	}

	// Remove any machines from maintenance that were set by this issue.
	for machine := range ms.state.Machines {
		totalMods += ms.UpdateMachine(machine, LeaveMaintenance, issue, project)
	}

	return totalMods
}

// prune removes any sites and machines from maintenance that no longer exist in
// siteinfo. A site will generally only disappear from siteinfo when it is
// retired.
func (ms *MaintenanceState) Prune(project string) {
	ms.mu.Lock()

	mods := false

	// Remove non-existent sites from maintenance, along with any machines.
	for site := range ms.state.Sites {
		_, err := ms.sites.Machines(site)
		if err != nil {
			updateMetrics(site, project, LeaveMaintenance, metrics.Site)
			delete(ms.state.Sites, site)
			for machine := range ms.state.Machines {
				if site == strings.Split(machine, "-")[1] {
					updateMetrics(machine, project, LeaveMaintenance, metrics.Machine)
					delete(ms.state.Machines, machine)
				}
			}
			mods = true
			log.Printf("Removed site %s from maintenace because it no longer exists", site)
		}
	}

	// Remove machines at non-existent sites from maintenance
	for machine := range ms.state.Machines {
		site := strings.Split(machine, "-")[1]
		_, err := ms.sites.Machines(site)
		if err != nil {
			for machine := range ms.state.Machines {
				if site == strings.Split(machine, "-")[1] {
					updateMetrics(machine, project, LeaveMaintenance, metrics.Machine)
					delete(ms.state.Machines, machine)
				}
			}
			mods = true
			log.Printf("Removed machine %s from maintenace because the site no longer exists", machine)
		}
	}

	// Unlock here, since ms.Write() does its own locking and unlocking.
	ms.mu.Unlock()

	// Only write state to file if the current state was modified.
	if mods {
		ms.Write()
	}
}

// New creates a MaintenanceState based on the passed-in filename. If it can't
// be restored from disk, it also generates an error.
func New(filename string, sites Sites, project string) (*MaintenanceState, error) {
	s := &MaintenanceState{
		state: state{
			Machines: make(map[string][]string),
			Sites:    make(map[string][]string),
		},
		filename: filename,
		sites:    sites,
	}
	err := s.Restore(project)
	if err != nil {
		log.Printf("WARNING: Failed to restore state file %s: %s", filename, err)
		metrics.Error.WithLabelValues("restore", "maintenancestate.New").Add(1)
	}
	return s, err
}
