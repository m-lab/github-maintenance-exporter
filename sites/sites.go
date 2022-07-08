package siteinfo

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/m-lab/go/siteinfo"
)

// Siteinfo implements the maintenancestate.Siteinfo interface.
type Siteinfo struct {
	Client  *siteinfo.Client
	Project string
	Sites   map[string][]string
}

// Machines takes a short site name parameter (e.g. abc02), and will return
// the machines (e.g., mlab1, mlab2) that the site contains.
func (s *Siteinfo) Machines(site string) ([]string, error) {
	machines, ok := s.Sites[site]
	if !ok {
		return []string{}, errors.New("site not found")
	}
	return machines, nil
}

// Reload reloads the siteinfo struct with fresh data from the siteinfo API. It
// is meant to be run periodically in some sort of loop.
func (s *Siteinfo) Reload(ctx context.Context) error {
	siteMachines, err := s.Client.SiteMachines()
	if err != nil {
		return err
	}
	s.Sites = siteMachines
	log.Println("INFO: successfully [re]loaded the siteinfo data.")
	return nil
}

func New(project string) *Siteinfo {
	client := siteinfo.New(project, "v2", &http.Client{})
	return &Siteinfo{
		Client:  client,
		Project: project,
	}
}
