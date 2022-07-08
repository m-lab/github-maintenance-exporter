package sites

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/m-lab/go/siteinfo"
	"github.com/m-lab/go/siteinfo/siteinfotest"
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

func TestReload(t *testing.T) {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	var testSites map[string][]string

	cachingClient := New("mlab-sandbox")

	httpProvider := &siteinfotest.StringProvider{
		Response: testSiteinfoData0,
	}
	cachingClient.Siteinfo = siteinfo.New(cachingClient.Project, "v2", httpProvider)
	err := cachingClient.Reload(ctx)
	if err != nil {
		t.Errorf("Unexpected error from Reload(): %v", err)
	}

	json.Unmarshal([]byte(testSiteinfoData0), &testSites)

	if !reflect.DeepEqual(cachingClient.Sites, testSites) {
		t.Errorf("Actual sites not equal to expected sites\ngot: %v\nwant:%v\n", cachingClient.Sites, testSites)
	}

	// Test that Reload() replaces all existing sites in cachingClient.Sites
	// with the values returned by siteinfo.SiteMachines().
	httpProvider = &siteinfotest.StringProvider{
		Response: testSiteinfoData1,
	}
	cachingClient.Siteinfo = siteinfo.New(cachingClient.Project, "v2", httpProvider)
	err = cachingClient.Reload(ctx)
	if err != nil {
		t.Errorf("Unexpected error from Reload(): %v", err)
	}

	if len(cachingClient.Sites) != 4 {
		t.Errorf("Expected 4 sites, but got: %d", len(cachingClient.Sites))
	}

	if _, ok := cachingClient.Sites["abc0t"]; ok {
		t.Error("Site abc0t should not be in sites, yet is.")
	}

	if len(cachingClient.Sites["omg09"]) != 2 {
		t.Errorf("Site omg09 should have 2 machines, but got: %d", len(cachingClient.Sites["omg09"]))
	}
}

func TestReloadWithError(t *testing.T) {
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	cachingClient := New("mlab-sandbox")
	// Test an error from cachingClient.Reload().
	cachingClient.Siteinfo = siteinfo.New(cachingClient.Project, "v2", &siteinfotest.FailingProvider{})
	err := cachingClient.Reload(ctx)
	if err == nil {
		t.Error("Expected an error from Reload(), but didn't get one", err)
	}

}

func TestMachines(t *testing.T) {
	cachingClient := New("mlab-sandbox")
	err := json.Unmarshal([]byte(testSiteinfoData0), &cachingClient.Sites)
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
		machines, err := cachingClient.Machines(tt.site)

		if (err != nil) != tt.wantError {
			t.Errorf("TestMachines(): error = %v, wantError %v", err, tt.wantError)
		}

		if len(machines) != tt.wantCount {
			t.Errorf("TestMachines(): wanted %d machines for site %s, but got %d", tt.wantCount, tt.site, len(machines))
		}
	}
}
