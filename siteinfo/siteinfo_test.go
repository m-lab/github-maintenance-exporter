package siteinfo

import (
	"encoding/json"
	"testing"
)

var testSiteinfoData = []byte(`{
	"abc0t": ["mlab1", "mlab2", "mlab3", "mlab4"],
	"xyz02": ["mlab1"],
	"lol01": ["mlab2", "mlab4"]
}`)

func TestSiteMachines(t *testing.T) {
	si := New("mlab-sandbox")
	err := json.Unmarshal(testSiteinfoData, &si.Sites)
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
