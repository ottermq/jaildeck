package freebsd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/system"
)

// From capture-vm-network-induced.sh (a private switch, two guests, NAT
// through a jaildeck/* anchor) and the PF anchor measurements recorded in
// JD-040, on isengard. Where a line is constructed, the test says so.

func TestSwitchesReadsAPrivateSwitchAndAShared(t *testing.T) {
	for capture, want := range map[string]networks.Switch{
		"vm_net_switch_list_private": {Name: "jd-netsw", Type: "standard", Bridge: "vm-jd-netsw", Address: "10.81.0.1/24", Private: true},
		"vm_mk_switch_list":          {Name: "jd-capsw", Type: "standard", Bridge: "vm-jd-capsw", Address: "10.77.0.1/24"},
	} {
		a := NewAdapter(&replayRunner{t: t, set: vmSet, captures: map[string]string{"vm switch list": capture}})
		switches, err := a.Switches(context.Background())
		if err != nil || len(switches) != 1 || switches[0] != want {
			t.Errorf("%s: %+v, %v; want %+v", capture, switches, err, want)
		}
	}
}

func TestSwitchesSaysVMBhyveIsNotConfigured(t *testing.T) {
	a := NewAdapter(&replayRunner{t: t, set: vmSet, captures: map[string]string{"vm switch list": "vm_installed_list"}})
	_, err := a.Switches(context.Background())
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusConflict || !strings.Contains(appErr.Message, "$vm_enable") {
		t.Errorf("err = %v, want a 409 in vm-bhyve's words", err)
	}
}

// Constructed: route's output with the gateway left out, which this test
// does not read and a public repository need not hold; and FreeBSD's message
// for a host with no default route.
func TestDefaultRouteInterface(t *testing.T) {
	a := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stdout: "   route to: default\ndestination: default\n       mask: default\n  interface: em0\n      flags: <UP,GATEWAY,DONE,STATIC>\n"}, nil
	}))
	if got, err := a.DefaultRouteInterface(context.Background()); err != nil || got != "em0" {
		t.Errorf("interface = %q, %v", got, err)
	}
	none := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
		return system.CommandResult{Stderr: "route: route has not been found\n", ExitCode: 1}, errors.New("exit status 1")
	}))
	if got, err := none.DefaultRouteInterface(context.Background()); err != nil || got != "" {
		t.Errorf("no route: %q, %v; want empty and no error", got, err)
	}
}

// The lines are the ones pfctl printed in the measurements: `nat-anchor
// "jaildeck/*" all` in the main ruleset, and the anchor's rule in pfctl's own
// form.
func TestPFReadsTheAnchorsAndTheRulesFile(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "pf.conf")
	if err := os.WriteFile(rules, []byte(`nat-anchor "jaildeck/*"`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		switch cmd.String() {
		case "pfctl -s info":
			return system.CommandResult{Stdout: "Status: Enabled for 0 days 00:01:06           Debug: Urgent\n"}, nil
		case "pfctl -s nat":
			return system.CommandResult{Stdout: "nat-anchor \"jaildeck/*\" all\n"}, nil
		case "sysrc -n pf_rules":
			return system.CommandResult{Stdout: rules + "\n"}, nil
		}
		t.Errorf("unexpected %s", cmd)
		return system.CommandResult{}, errors.New("unexpected")
	}))
	pf, err := a.PF(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !pf.Loaded || !pf.Enabled || !pf.NatAnchor || pf.RulesFile != rules ||
		!strings.Contains(pf.RulesText, "nat-anchor") {
		t.Errorf("pf = %+v", pf)
	}
}

// pfctl not loaded: /dev/pf is missing, which is an answer (the same
// capture Host Setup's PF check reads).
func TestPFNotLoadedIsAnAnswer(t *testing.T) {
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		if cmd.String() == "pfctl -s info" {
			return replay(t, "isengard", "pfctl_info")
		}
		return system.CommandResult{Stdout: "/etc/pf.conf\n"}, nil
	}))
	pf, err := a.PF(context.Background())
	if err != nil || pf.Loaded || pf.Enabled {
		t.Errorf("pf = %+v, %v; want not loaded", pf, err)
	}
}

func TestLoadAnchorValidatesFirst(t *testing.T) {
	var ran []string
	refuse := false
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		ran = append(ran, cmd.String())
		if refuse && slices.Contains(cmd.Args, "-n") {
			// Measured: pfctl's answer to a /33 in the anchor's file.
			return system.CommandResult{Stderr: "netmask 33 invalid for IPv4 address\nvmnet.conf:1: skipping rule due to errors\n", ExitCode: 1},
				errors.New("exit status 1")
		}
		return system.CommandResult{}, nil
	}))
	command, err := a.LoadAnchor(context.Background(), "jaildeck/vmnet", "/x/vmnet.conf")
	want := []string{"pfctl -n -a jaildeck/vmnet -f /x/vmnet.conf", "pfctl -a jaildeck/vmnet -f /x/vmnet.conf"}
	if err != nil || !slices.Equal(ran, want) || command != want[0]+" && "+want[1] {
		t.Errorf("ran %v, command %q, err %v", ran, command, err)
	}

	ran, refuse = nil, true
	_, err = a.LoadAnchor(context.Background(), "jaildeck/vmnet", "/x/vmnet.conf")
	if err == nil || !strings.Contains(err.Error(), "netmask 33") || len(ran) != 1 {
		t.Errorf("a refused file: ran %v, err %v; want pfctl's reason and no load", ran, err)
	}
}

func TestAnchorFileAndWriteAnchorFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pf", "vmnet.conf")
	a := NewAdapter(nil)
	if got, err := a.AnchorFile(context.Background(), path); err != nil || got != "" {
		t.Errorf("missing file = %q, %v; want empty", got, err)
	}
	if _, err := a.WriteAnchorFile(context.Background(), path, "nat on em0 inet from 10.88.0.0/24 to any -> (em0)\n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.AnchorFile(context.Background(), path); !strings.HasPrefix(got, "nat on em0") {
		t.Errorf("after writing: %q", got)
	}
}

func TestSwitchWritesAreVMBhyvesCommands(t *testing.T) {
	var ran []string
	a := NewAdapter(runnerFunc(func(_ context.Context, cmd system.Command) (system.CommandResult, error) {
		ran = append(ran, cmd.String())
		return system.CommandResult{}, nil
	}))
	ctx := context.Background()
	_, _ = a.CreateSwitch(ctx, "jaildeck")
	_, _ = a.SetSwitchAddress(ctx, "jaildeck", "10.88.0.1/24")
	_, _ = a.SetSwitchPrivate(ctx, "jaildeck")
	_, _ = a.SetSysctl(ctx, "net.inet.ip.forwarding", "1")
	_, _ = a.SetRCConf(ctx, "gateway_enable", "YES")
	want := []string{"vm switch create jaildeck", "vm switch address jaildeck 10.88.0.1/24",
		"vm switch private jaildeck on", "sysctl net.inet.ip.forwarding=1", "sysrc gateway_enable=YES"}
	if !slices.Equal(ran, want) {
		t.Errorf("ran %v\nwant %v", ran, want)
	}
}

// Constructed: a switch with no address, which vm switch list prints as "-"
// in the format of vm_mk_switch_list.
func TestASwitchWithNoAddress(t *testing.T) {
	got := parseSwitchList("NAME      TYPE      IFACE        ADDRESS  PRIVATE  MTU  VLAN  PORTS\n" +
		"jaildeck  standard  vm-jaildeck  -        no       -    -     -\n")
	if len(got) != 1 || got[0].Address != "" || got[0].Bridge != "vm-jaildeck" || got[0].Private {
		t.Errorf("switches = %+v, want one with no address", got)
	}
}
