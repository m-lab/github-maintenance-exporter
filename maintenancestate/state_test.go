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
			"mlab1.abc01.measurement-lab.org": ["1"],
			"mlab1.abc02.measurement-lab.org": ["8"],
			"mlab2.abc02.measurement-lab.org": ["8"],
			"mlab3.abc02.measurement-lab.org": ["8"],
			"mlab4.abc02.measurement-lab.org": ["8"],
			"mlab3.def01.measurement-lab.org": ["5"],
			"mlab1.uvw03.measurement-lab.org": ["4", "11"],
			"mlab2.uvw03.measurement-lab.org": ["4", "11"],
			"mlab3.uvw03.measurement-lab.org": ["4", "11"],
			"mlab4.uvw03.measurement-lab.org": ["4", "11"]
		},
		"Sites": {
			"abc02": ["8"],
			"uvw03": ["4", "11"]
		}
	}
`

func TestRestore(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestRestore")
	rtx.Must(err, "Could not create tempdir")
	defer os.RemoveAll(dir)
	rtx.Must(ioutil.WriteFile(dir+"/state.json", []byte(savedState), 0644), "Could not write state to tempfile")

	s, err := New(dir + "/state.json")
	rtx.Must(err, "Could not restore state")
	expectedMachines := 10
	expectedSites := 2

	if len(s.Machines) != expectedMachines {
		t.Errorf("restoreState(): Expected %d restored machines; have %d.",
			expectedMachines, len(s.Machines))
	}

	if len(s.Sites) != expectedSites {
		t.Errorf("restoreState(): Expected %d restored sites; have %d.",
			expectedSites, len(s.Sites))
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
	s1.Machines["mlab1.abc01.measurement-lab.org"] = append(s1.Machines["mlab1.abc01.measurement-lab.org"], "2")
	rtx.Must(s1.Write(), "Could not save state")

	s2, err := New(dir + "/savedstate.json")
	rtx.Must(err, "Could not restore state for s2")
	if !reflect.DeepEqual(*s2, *s1) {
		t.Error("The state was not the same after write/restore:", s1, s2)
	}
	if strings.Join(s2.Machines["mlab1.abc01.measurement-lab.org"], " ") != "1 2" {
		t.Error("s2 was not different from the initial (not the saved and modified) input.", s2.Machines["mlab1.abc01.measurement-lab.org"])
	}

	// Now exercise the error cases
	s2.filename = ""
	err = s2.Write()
	if err == nil {
		t.Error("Should have had an error when writing s2 with an empty filename")
	}
}
