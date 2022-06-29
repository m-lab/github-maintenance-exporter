package siteinfo

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/m-lab/go/siteinfo"
)

// Siteinfo defines a new siteinfo interface for interacting with the siteinfo API.
type Siteinfo interface {
	Reload(ctx context.Context) error
	SiteMachines(site string) ([]string, error)
}

// SiteinfoClient implements the Siteinfo interface.
type SiteinfoClient struct {
	Client  *siteinfo.Client
	Project string
	Sites   map[string][]string
}

// SiteMachines takes a short site name parameter (e.g. abc02), and will return
// the machines (e.g., mlab1, mlab2) that the site contains.
func (s *SiteinfoClient) SiteMachines(site string) ([]string, error) {
	machines, ok := s.Sites[site]
	if !ok {
		return []string{}, errors.New("site not found")
	}
	return machines, nil
}

// Reload reloads the siteinfo struct with fresh data from siteinfo. It is meant
// to be run periodically in some sort of loop. The "url" parameter is the URL
// where the siteinfo JSON document can be downloaded.
func (s *SiteinfoClient) Reload(ctx context.Context) error {
	siteMachines, err := s.Client.SiteMachines()
	if err != nil {
		return err
	}
	s.Sites = siteMachines
	log.Println("INFO: successfully [re]loaded the siteinfo data.")
	return nil
}

func New(project string) *SiteinfoClient {
	client := siteinfo.New(project, "v2", &http.Client{})
	return &SiteinfoClient{
		Client:  client,
		Project: project,
	}
}
