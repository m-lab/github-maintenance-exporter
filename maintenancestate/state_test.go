package maintenancestate

import (
	"io/ioutil"
	"os"
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
	f, err := ioutil.TempFile("", "TestRestore")
	rtx.Must(err, "Could not create tempfile")
	fname := f.Name()
	defer os.Remove(fname)
	rtx.Must(ioutil.WriteFile(fname, []byte(savedState), 0644), "Could not write state to tempfile")

	s := New(fname)
	rtx.Must(s.Restore(), "Could not restore state")
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
}

func TestWrite(t *testing.T) {

}
