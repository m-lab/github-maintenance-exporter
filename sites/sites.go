package sites

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"

	"github.com/m-lab/go/siteinfo"
)

// CachingClient implements the maintenancestate.Sites interface.
type CachingClient struct {
	mu       sync.Mutex
	Siteinfo *siteinfo.Client
	Project  string
	Sites    map[string][]string
}

// Machines takes a short site name parameter (e.g. abc02), and will return
// the machines (e.g., mlab1, mlab2) that the site contains.
func (cc *CachingClient) Machines(site string) ([]string, error) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	machines, ok := cc.Sites[site]
	if !ok {
		return []string{}, errors.New("site not found")
	}
	return machines, nil
}

// Reload reloads CachingClient.Sites with fresh data from the siteinfo API. It
// is meant to be run periodically in some sort of loop.
func (cc *CachingClient) Reload(ctx context.Context) error {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	siteMachines, err := cc.Siteinfo.SiteMachines()
	if err != nil {
		return err
	}
	cc.Sites = siteMachines
	log.Println("INFO: successfully [re]loaded the siteinfo data.")
	return nil
}

func New(project string) *CachingClient {
	siteinfo := siteinfo.New(project, "v2", &http.Client{})
	return &CachingClient{
		Siteinfo: siteinfo,
		Project:  project,
	}
}
