package freebsd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/system"
)

// The commands a limits change runs (#119), and what the host answered them
// in capture-limits-induced.sh on isengard.

func capturedText(t *testing.T, name, ext string) string {
	t.Helper()
	out, err := os.ReadFile(fixture("isengard-induced", name, ext))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestAddRule(t *testing.T) {
	adapter := newRctlAdapter(t)
	command, err := adapter.AddRule(context.Background(), "jail:db1:memoryuse:deny=1G")
	if err != nil {
		t.Fatal(err)
	}
	if command != "rctl -a jail:db1:memoryuse:deny=1G" || !reflect.DeepEqual(adapter.ran, []string{command}) {
		t.Errorf("command %q, ran %v", command, adapter.ran)
	}
}

func TestRemoveRule(t *testing.T) {
	adapter := newRctlAdapter(t)
	command, err := adapter.RemoveRule(context.Background(), "jail:db1:memoryuse:deny")
	if err != nil || command != "rctl -r jail:db1:memoryuse:deny" {
		t.Errorf("command %q, err %v", command, err)
	}
}

// Measured: a filter that matches nothing exits 1 with "No such process".
func TestRemoveRuleTreatsNothingToRemoveAsDone(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["rctl -r jail:db1:openfiles:deny"] = system.CommandResult{
		ExitCode: 1, Stderr: capturedText(t, "lim_remove_absent", "stderr"),
	}
	if _, err := adapter.RemoveRule(context.Background(), "jail:db1:openfiles:deny"); err != nil {
		t.Fatalf("RemoveRule: %v", err)
	}
}

func TestRemoveRulePassesOnARealFailure(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["rctl -r jail:db1:openfiles:deny"] = system.CommandResult{
		ExitCode: 1, Stderr: "rctl: failed to remove rule 'jail:db1:openfiles:deny': Device busy",
	}
	if _, err := adapter.RemoveRule(context.Background(), "jail:db1:openfiles:deny"); err == nil {
		t.Fatal("want the error")
	}
}

func TestSetCPUSet(t *testing.T) {
	adapter := newRctlAdapter(t)
	for list, want := range map[string]string{"0-3": "cpuset -l 0-3 -j db1", "": "cpuset -l all -j db1"} {
		command, err := adapter.SetCPUSet(context.Background(), "db1", list)
		if err != nil || command != want {
			t.Errorf("%q: command %q, err %v", list, command, err)
		}
	}
}

// Measured: a cpu the host does not have.
func TestSetCPUSetCarriesTheHostsRefusal(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["cpuset -l 16 -j db1"] = system.CommandResult{
		ExitCode: 1, Stderr: capturedText(t, "lim_cpuset_too_many", "stderr"),
	}
	_, err := adapter.SetCPUSet(context.Background(), "db1", "16")
	if err == nil || !strings.Contains(err.Error(), "Invalid argument") {
		t.Errorf("err %v", err)
	}
}

func TestRuleUsage(t *testing.T) {
	adapter := newRctlAdapter(t)
	adapter.answer["rctl -u jail:jdcap119"] = system.CommandResult{Stdout: capturedText(t, "lim_usage_hogs", "stdout")}
	usage, err := adapter.RuleUsage(context.Background(), "jdcap119")
	if err != nil {
		t.Fatal(err)
	}
	if usage["memoryuse"] != 547676160 || usage["maxproc"] != 4 {
		t.Errorf("usage %v", usage)
	}
}

// A rule or filter reaches the host only as jail:<one name>:...
func TestLimitCommandsRefuseAnotherSubject(t *testing.T) {
	adapter := newRctlAdapter(t)
	ctx := context.Background()
	for _, rule := range []string{"", "jail:", "jail::memoryuse:deny=1G", "memoryuse:deny=1G",
		"user:root:memoryuse:deny=1G", "jail:db1 db2:memoryuse:deny=1G", "jail:-x:maxproc:deny=1"} {
		if _, err := adapter.AddRule(ctx, rule); err == nil {
			t.Errorf("AddRule accepted %q", rule)
		}
		if _, err := adapter.RemoveRule(ctx, rule); err == nil {
			t.Errorf("RemoveRule accepted %q", rule)
		}
	}
	for _, name := range []string{"", "-l", "db1 db2"} {
		if _, err := adapter.SetCPUSet(ctx, name, "1"); err == nil {
			t.Errorf("SetCPUSet accepted %q", name)
		}
	}
	if len(adapter.ran) != 0 {
		t.Errorf("reached the host: %v", adapter.ran)
	}
}

// --- rewriting the stanza ------------------------------------------------

func TestReplaceConfig(t *testing.T) {
	a := &Adapter{confDir: t.TempDir()}
	path := filepath.Join(a.confDir, "db1.conf")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := a.ReplaceConfig(context.Background(), "db1", "old", "new")
	if err != nil || got != path {
		t.Fatalf("path %q, err %v", got, err)
	}
	content, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(content) != "new" || info.Mode().Perm() != 0o640 {
		t.Errorf("content %q, mode %v", content, info.Mode().Perm())
	}
	entries, _ := os.ReadDir(a.confDir)
	if len(entries) != 1 {
		t.Errorf("left %d files behind", len(entries))
	}
}

// A file changed since it was read is somebody else's change.
func TestReplaceConfigRefusesAChangedFile(t *testing.T) {
	a := &Adapter{confDir: t.TempDir()}
	path := filepath.Join(a.confDir, "db1.conf")
	if err := os.WriteFile(path, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := a.ReplaceConfig(context.Background(), "db1", "old", "new")
	if err == nil || !strings.Contains(err.Error(), "changed while") {
		t.Fatalf("err %v", err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "edited by hand" {
		t.Errorf("overwrote it: %q", content)
	}
}

func TestReplaceConfigOfAMissingFile(t *testing.T) {
	a := &Adapter{confDir: t.TempDir()}
	if _, err := a.ReplaceConfig(context.Background(), "db1", "old", "new"); err == nil {
		t.Fatal("replaced a file that is not there")
	}
	if _, err := a.ReplaceConfig(context.Background(), "../db1", "old", "new"); err == nil {
		t.Fatal("accepted a path for a name")
	}
}
