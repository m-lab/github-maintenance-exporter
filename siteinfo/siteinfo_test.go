package siteinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"testing"

	"github.com/m-lab/go/siteinfo"
)

var testSiteinfoData0 = `{
	"abc0t": ["mlab1", "mlab2", "mlab3", "mlab4"],
	"xyz02": ["mlab1"],
	"lol01": ["mlab2", "mlab4"]
}`

var testSiteinfoData1 = `{
	"omg09": ["mlab1", "mlab2"],
	"abc06": ["mlab1"],
	"xyz99": ["mlab1", "mlab2", "mlab3", "mlab4"],
	"diy03": ["mlab1", "mlab3", "mlab4"]
}`

// The following "Provider" types were nicked directly from go/siteinfo:
// https://github.com/m-lab/go/blob/master/siteinfo/client_test.go#L13

// stringProvider implements a siteinfo.HTTPProvider but the response's content
// is a fixed string.
type stringProvider struct {
	response string
}

func (prov stringProvider) Get(string) (*http.Response, error) {
	return &http.Response{
		Body:       ioutil.NopCloser(bytes.NewBufferString(prov.response)),
		StatusCode: http.StatusOK,
	}, nil
}

// failingProvider implements a siteinfo.HTTPProvider, but always return an error.
type failingProvider struct{}

func (prov failingProvider) Get(string) (*http.Response, error) {
	return nil, fmt.Errorf("error")
}

func TestReload(t *testing.T) {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	var testSites map[string][]string

	si := New("mlab-sandbox")

	httpProvider := &stringProvider{
		response: testSiteinfoData0,
	}
	si.Client = siteinfo.New(si.Project, "v2", httpProvider)
	err := si.Reload(ctx)
	if err != nil {
		t.Errorf("Unexpected error from Reload(): %v", err)
	}

	json.Unmarshal([]byte(testSiteinfoData0), &testSites)

	if !reflect.DeepEqual(si.Sites, testSites) {
		t.Errorf("Actual sites not equal to expected sites\ngot: %v\nwant:%v\n", si.Sites, testSites)
	}

	// Test that Reload() replaces all existing sites in si.Sites with the
	// values returned by siteinfo.SiteMachines().
	httpProvider = &stringProvider{
		response: testSiteinfoData1,
	}
	si.Client = siteinfo.New(si.Project, "v2", httpProvider)
	err = si.Reload(ctx)
	if err != nil {
		t.Errorf("Unexpected error from Reload(): %v", err)
	}

	if len(si.Sites) != 4 {
		t.Errorf("Expected 4 sites, but got: %d", len(si.Sites))
	}

	if _, ok := si.Sites["abc0t"]; ok {
		t.Error("Site abc0t should not be in sites, yet is.")
	}

	if len(si.Sites["omg09"]) != 2 {
		t.Errorf("Site omg09 should have 2 machines, but got: %d", len(si.Sites["omg09"]))
	}
}

func TestReloadWithError(t *testing.T) {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	si := New("mlab-sandbox")
	// Test an error from si.Reload().
	si.Client = siteinfo.New(si.Project, "v2", &failingProvider{})
	err := si.Reload(ctx)
	if err == nil {
		t.Error("Expected an error from Reload(), but didn't get one", err)
	}

}

func TestSiteMachines(t *testing.T) {
	si := New("mlab-sandbox")
	err := json.Unmarshal([]byte(testSiteinfoData0), &si.Sites)
	if err != nil {
		t.Errorf("failed to unmarshall test json: %v", err)
	}

	tests := []struct {
		name      string
		site      string
		wantCount int
		wantError bool
	}{
		{
			name:      "typical-4-machine-site",
			site:      "abc0t",
			wantCount: 4,
			wantError: false,
		},
		{
			name:      "single-machine-virtual-site",
			site:      "xyz02",
			wantCount: 1,
			wantError: false,
		},
		{
			name:      "oddball-two-machine-site",
			site:      "lol01",
			wantCount: 2,
			wantError: false,
		},
		{
			name:      "nonexistent-site",
			site:      "qqq07",
			wantCount: 0,
			wantError: true,
		},
	}

	for _, tt := range tests {
		machines, err := si.SiteMachines(tt.site)

		if (err != nil) != tt.wantError {
			t.Errorf("TestSiteMachines(): error = %v, wantError %v", err, tt.wantError)
		}

		if len(machines) != tt.wantCount {
			t.Errorf("TestSiteMachines(): wanted %d machines for site %s, but got %d", tt.wantCount, tt.site, len(machines))
		}
	}
}
