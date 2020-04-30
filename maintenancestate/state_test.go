package maintenancestate

import (
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/m-lab/go/rtx"
)

// Sample maintenance state as written to disk in JSON format.
var savedState = `
	{
		"Machines": {
			"mlab1-abc01": ["1"],
			"mlab1-abc02": ["8"],
			"mlab2-abc02": ["8"],
			"mlab3-abc02": ["8"],
			"mlab4-abc02": ["8"],
			"mlab3-def01": ["5"],
			"mlab4-def01": ["20"],
			"mlab1-uvw03": ["4", "11"],
			"mlab2-uvw03": ["4", "11"],
			"mlab3-uvw03": ["4", "11"],
			"mlab4-uvw03": ["4", "11"]
		},
		"Sites": {
			"abc02": ["8"],
			"uvw03": ["4", "11"]
		}
	}
`

func TestActionStatus(t *testing.T) {
	if EnterMaintenance.StatusValue() != 1 || LeaveMaintenance.StatusValue() != 0 {
		t.Error(EnterMaintenance.StatusValue(), "and", LeaveMaintenance.StatusValue(), "should be 1 and 0")
	}
}

func TestUpdateStateWithBadValue(t *testing.T) {
	updateState(nil, "", nil, "", -1, "no-project") // The -1 should not be a legal action.
}

func TestUpdateMachine(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestUpdateMachine")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/state.json", []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := New(dir + "/state.json")
	rtx.Must(err, "Could not read from tmpfile")

	s.UpdateMachine("mlab3-def01", EnterMaintenance, "13", "mlab-oti")
	s.UpdateMachine("mlab3-def01", EnterMaintenance, "13", "mlab-oti")
	if len(s.state.Machines["mlab3-def01"]) != 2 {
		t.Error("Should have two items in", s.state.Machines["mlab3-def01"])
	}
	s.UpdateMachine("mlab3-def01", LeaveMaintenance, "5", "mlab-oti")
	if len(s.state.Machines["mlab3-def01"]) != 1 {
		t.Error("Should have one item in", s.state.Machines["mlab3-def01"])
	}
	s.UpdateMachine("mlab3-def01", LeaveMaintenance, "5", "mlab-oti")
	s.UpdateMachine("mlab3-def01", LeaveMaintenance, "13", "mlab-oti")

	if _, ok := s.state.Machines["mlab3-def01"]; ok {
		t.Errorf("%q was supposed to be deleted from %+v", "mlab3-def01", s)
	}
}

func TestCreateNodeLabel(t *testing.T) {
	tests := []struct {
		name      string
		shortNode string
		version   string
		project   string
		expected  string
	}{
		{
			name:      "create-v1-label-from-dotted-name",
			shortNode: "mlab4.abc02",
			version:   "v1",
			project:   "mlab-staging",
			expected:  "mlab4.abc02.measurement-lab.org",
		},
		{
			name:      "create-v1-label-from-flat-name",
			shortNode: "mlab1-abc02",
			version:   "v1",
			project:   "mlab-oti",
			expected:  "mlab1.abc02.measurement-lab.org",
		},
		{
			name:      "create-v2-label-from-dotted-name",
			shortNode: "mlab1.abc02",
			version:   "v2",
			project:   "mlab-oti",
			expected:  "mlab1-abc02.mlab-oti.measurement-lab.org",
		},
		{
			name:      "create-v2-label-from-flat-name",
			shortNode: "mlab1-abc0t",
			version:   "v2",
			project:   "mlab-sandbox",
			expected:  "mlab1-abc0t.mlab-sandbox.measurement-lab.org",
		},
		{
			name:      "create-label-with-no-version",
			shortNode: "mlab1.abc02",
			version:   "",
			project:   "mlab-oti",
			expected:  "mlab1-abc02.mlab-oti.measurement-lab.org",
		},
	}

	for _, test := range tests {
		actual := createNodeLabel(test.shortNode, test.project, test.version)

		if actual != test.expected {
			t.Errorf("createNodeLabel(): Expected label %s; got %s", test.expected, actual)
		}
	}
}

func TestUpdateSite(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestUpdateSite")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/state.json", []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := New(dir + "/state.json")
	rtx.Must(err, "Could not read from tmpfile")

	if _, ok := s.state.Sites["def01"]; ok {
		t.Error("Should not have def01 in sites.")
	}
	s.UpdateSite("def01", LeaveMaintenance, "20", "mlab-oti")
	if _, ok := s.state.Sites["def01"]; ok {
		t.Error("Should still not have def01 in sites.")
	}
	s.UpdateSite("def01", EnterMaintenance, "20", "mlab-oti")
	if len(s.state.Sites["def01"]) != 1 {
		t.Error("Should have one issue for def01")
	}
	if len(s.state.Machines["mlab1-def01"]) != 1 {
		t.Error("Should have one issue for mlab1-def01")
	}
	if len(s.state.Machines["mlab2-def01"]) != 1 {
		t.Error("Should have one issue for mlab2-def01")
	}
	if len(s.state.Machines["mlab3-def01"]) != 2 {
		t.Error("Should have two issues for mlab3-def01")
	}
	if len(s.state.Machines["mlab4-def01"]) != 1 {
		t.Error("Should have one issue for mlab4-def01")
	}
	s.UpdateSite("def01", LeaveMaintenance, "20", "mlab-oti")
	if _, ok := s.state.Sites["def01"]; ok {
		t.Error("Should not have def01 in sites.")
	}
	if _, ok := s.state.Machines["mlab1-def01"]; ok {
		t.Error("Should have nothing for mlab1-def01")
	}
	if _, ok := s.state.Machines["mlab2-def01"]; ok {
		t.Error("Should have nothing for mlab2-def01")
	}
	if len(s.state.Machines["mlab3-def01"]) != 1 {
		t.Error("Should have one issue for mlab3-def01")
	}
	s.UpdateSite("def01", EnterMaintenance, "25", "mlab-staging")
	if len(s.state.Sites["def01"]) != 1 {
		t.Error("Should have one issue for def01")
	}
	if _, ok := s.state.Machines["mlab1-def01"]; ok {
		t.Error("Should have nothing for mlab1-def01")
	}
	if _, ok := s.state.Machines["mlab2-def01"]; ok {
		t.Error("Should have nothing for mlab2-def01")
	}
	if len(s.state.Machines["mlab3-def01"]) != 1 {
		t.Error("Should have one issue for mlab3-def01")
	}
	if len(s.state.Machines["mlab4-def01"]) != 2 {
		t.Error("Should have two issues for mlab4-def01")
	}
	s.UpdateSite("def01", EnterMaintenance, "7", "mlab-sandbox")
	if len(s.state.Sites["def01"]) != 2 {
		t.Error("Should have two issues for def01")
	}
	if len(s.state.Machines["mlab1-def01"]) != 1 {
		t.Error("Should have one issue for mlab1-def01")
	}
	if len(s.state.Machines["mlab2-def01"]) != 1 {
		t.Error("Should have one issue for mlab2-def01")
	}
	if len(s.state.Machines["mlab3-def01"]) != 2 {
		t.Error("Should have two issues for mlab3-def01")
	}
	if len(s.state.Machines["mlab4-def01"]) != 3 {
		t.Error("Should have three issues for mlab4-def01")
	}
}

func TestCloseIssue(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestCloseIssue")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/state.json", []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := New(dir + "/state.json")
	rtx.Must(err, "Could not read from tmpfile")

	tests := []struct {
		name              string
		issue             string
		expectedMods      int
		closedMaintenance int
	}{
		{
			name:              "one-issue-per-entity-closes-maintenance",
			issue:             "8",
			expectedMods:      5,
			closedMaintenance: 5,
		},
		{
			name:              "multiple-issues-per-entity-does-not-close-maintenance",
			issue:             "4",
			expectedMods:      5,
			closedMaintenance: 0,
		},
		{
			name:              "close-issue-also-closes-machine-issues",
			issue:             "5",
			expectedMods:      1,
			closedMaintenance: 1,
		},
	}

	for _, test := range tests {
		rtx.Must(s.Restore(), "Could not restore state from tempfile")

		totalEntitiesBefore := len(s.state.Machines) + len(s.state.Sites)
		mods := s.CloseIssue(test.issue, "mlab-oti")
		totalEntitiesAfter := len(s.state.Machines) + len(s.state.Sites)
		closedMaintenance := totalEntitiesBefore - totalEntitiesAfter

		if mods != test.expectedMods {
			t.Errorf("closeIssue(): Expected %d state modifications; got %d",
				test.expectedMods, mods)
		}

		if closedMaintenance != test.closedMaintenance {
			t.Errorf("closeIssue(): Expected %d closed maintenances; got %d",
				test.closedMaintenance, closedMaintenance)
		}
	}
}

func TestRestore(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestRestore")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/state.json", []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := New(dir + "/state.json")
	rtx.Must(err, "Could not restore state")
	expectedMachines := 11
	expectedSites := 2

	if len(s.state.Machines) != expectedMachines {
		t.Errorf("restoreState(): Expected %d restored machines; have %d.",
			expectedMachines, len(s.state.Machines))
	}

	if len(s.state.Sites) != expectedSites {
		t.Errorf("restoreState(): Expected %d restored sites; have %d.",
			expectedSites, len(s.state.Sites))
	}

	// Now exercise the error cases
	s2, err := New(dir + "/doesnotexist.json")
	if s2 == nil || err == nil {
		t.Error("Should have received a non-nil state and a non-nil error, but got:", s2, err)
	}

	rtx.Must(ioutil.WriteFile(dir+"/badcontents.json", []byte("This is not json"), 0644), "Could not write bad data for test")
	s3, err := New(dir + "/badcontents.json")
	if s3 == nil || err == nil {
		t.Error("Should have received a non-nil state and a non-nil error, but got:", s3, err)
	}
}

func TestWrite(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestWrite")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/savedstate.json", []byte(savedState), 0644), "Could not write to file")

	s1, err := New(dir + "/savedstate.json")
	rtx.Must(err, "Could not restore state for s1")
	s1.UpdateMachine("mlab1-abc01", EnterMaintenance, "2", "mlab-oti")
	rtx.Must(s1.Write(), "Could not save state")

	s2, err := New(dir + "/savedstate.json")
	rtx.Must(err, "Could not restore state for s2")
	if !reflect.DeepEqual(*s2, *s1) {
		t.Error("The state was not the same after write/restore:", s1, s2)
	}
	if strings.Join(s2.state.Machines["mlab1-abc01"], " ") != "1 2" {
		t.Error("s2 was not different from the initial (not the saved and modified) input.", s2.state.Machines["mlab1-abc01"])
	}

	// Now exercise the error cases
	s2.filename = ""
	err = s2.Write()
	if err == nil {
		t.Error("Should have had an error when writing s2 with an empty filename")
	}
}
