package maintenancestate

import (
	"encoding/json"
	"io/ioutil"
	"log"

	"github.com/m-lab/github-maintenance-exporter/metrics"
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

func (s *MaintenanceState) Restore() error {
	data, err := ioutil.ReadFile(s.filename)
	if err != nil {
		log.Printf("ERROR: Failed to read state data from %s: %s", s.filename, err)
		metrics.Error.WithLabelValues("readfile", "restoreState").Inc()
		return err
	}

	err = json.Unmarshal(data, &s)
	if err != nil {
		log.Printf("ERROR: Failed to unmarshal JSON: %s", err)
		metrics.Error.WithLabelValues("unmarshaljson", "restoreState").Inc()
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
	if err != nil {
		log.Printf("ERROR: Failed to marshal JSON: %s", err)
		metrics.Error.WithLabelValues("marshaljson", "writeState").Add(1)
		return err
	}

	err = ioutil.WriteFile(s.filename, data, 0664)
	if err != nil {
		log.Printf("ERROR: Failed to write state to %s: %s", s.filename, err)
		metrics.Error.WithLabelValues("writefile", "writeState").Add(1)
		return err
	}

	log.Printf("INFO: Successfully wrote state to %s.", s.filename)
	return nil
}

func New(filename string) *MaintenanceState {
	s := &MaintenanceState{
		Machines: make(map[string][]string),
		Sites:    make(map[string][]string),
		filename: filename,
	}
	err := s.Restore()
	if err != nil {
		log.Printf("WARNING: Failed to open state file %s: %s", filename, err)
		metrics.Error.WithLabelValues("openfile", "main").Add(1)
	}
	return s
}
