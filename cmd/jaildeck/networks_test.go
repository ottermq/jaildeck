package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/networks"
)

// The fake host has no record: restore says so and changes nothing.
func TestRunNetworksRestoreWithNothingRecorded(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := runNetworks([]string{"restore"}, &out, &errOut, initConfig(t)); err != nil {
		t.Fatalf("runNetworks(restore) = %v", err)
	}
	if want := "no tenant networks are recorded: nothing to do\n"; out.String() != want || errOut.Len() != 0 {
		t.Errorf("out = %q, errOut = %q", out.String(), errOut.String())
	}
}

func TestRunNetworksRejects(t *testing.T) {
	for _, args := range [][]string{nil, {"list"}, {"restore", "now"}} {
		if err := runNetworks(args, io.Discard, io.Discard, initConfig(t)); !errors.Is(err, ErrUnexpectedArgs) {
			t.Errorf("runNetworks(%v) = %v, want ErrUnexpectedArgs", args, err)
		}
	}
}

func TestPrintRestored(t *testing.T) {
	var out, errOut bytes.Buffer
	printRestored(&out, &errOut, networks.Restored{
		Created:  []networks.Recorded{{Name: "acme", Bridge: "jdn0", Gateway: "10.92.0.1/24"}},
		Present:  []networks.Recorded{{Name: "globex", Bridge: "jdn1", Gateway: "10.92.1.1/24"}},
		Problems: []string{"jdn2 (initech): bridge create failed"},
	})
	want := "restored jdn0: the network acme, gateway 10.92.0.1/24\njdn1 is there: the network globex\n"
	if out.String() != want || !strings.Contains(errOut.String(), "jdn2 (initech)") {
		t.Errorf("out = %q, errOut = %q", out.String(), errOut.String())
	}
}
