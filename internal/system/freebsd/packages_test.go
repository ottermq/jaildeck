package freebsd

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// pkg -c is a host command against a directory, not jexec against a jail --
// which is the whole reason deriving a template is not the exec endpoint. On
// the deployment target, `jexec 15.1-RELEASE ls /` answers "jail not found"
// while `pkg -c /usr/local/jails/templates/15.1-RELEASE info` exits 0.
func TestInstallCommand(t *testing.T) {
	runner := &recordingRunner{}
	if _, err := NewAdapter(runner).Install(context.Background(),
		"/usr/local/jails/templates/postgres-16",
		[]string{"postgresql16-server", "postgresql16-client"}); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if len(runner.ran) != 1 {
		t.Fatalf("ran %d commands, want 1", len(runner.ran))
	}
	if runner.ran[0].Name != "pkg" {
		t.Errorf("ran %q, want pkg", runner.ran[0].Name)
	}
	want := []string{"-c", "/usr/local/jails/templates/postgres-16", "install", "-y",
		"postgresql16-server", "postgresql16-client"}
	if !reflect.DeepEqual(runner.ran[0].Args, want) {
		t.Errorf("args =\n  %q\nwant\n  %q", runner.ran[0].Args, want)
	}
	// -y because there is no terminal to answer a prompt on, and a package
	// manager waiting for input is a request that never returns.
	if runner.ran[0].Timeout == 0 {
		t.Error("no timeout; pkg fetches from a mirror and must still be bounded")
	}
}

// A failed install still reports the command, which is what names the package
// pkg could not find in the audit log.
func TestInstallReportsTheCommandOnFailure(t *testing.T) {
	runner := &recordingRunner{err: errNotFound}
	result, err := NewAdapter(runner).Install(context.Background(), "/t", []string{"nope"})
	if err == nil {
		t.Fatal("Install() returned no error")
	}
	if result.Command != "pkg -c /t install -y nope" || result.Root != "/t" {
		t.Errorf("result = %+v, want the command it tried", result)
	}
}

var errNotFound = errors.New("exit status 70")

// The package guard's reads and its one write (#264), as run on the
// reference host: pkg -c against the template's root.
func TestPackageGuardCommands(t *testing.T) {
	runner := &recordingRunner{stdout: "postgresql18-server\npgbouncer\n"}
	adapter := NewAdapter(runner)
	ctx := context.Background()

	explicit, err := adapter.Explicit(ctx, "/t")
	if err != nil || !reflect.DeepEqual(explicit, []string{"pgbouncer", "postgresql18-server"}) {
		t.Errorf("Explicit = %q, %v", explicit, err)
	}
	if _, err := adapter.Orphans(ctx, "/t"); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.MarkExplicit(ctx, "/t", "postgresql18-server")
	if err != nil || result.Command != "pkg -c /t set -y -A 0 postgresql18-server" {
		t.Errorf("MarkExplicit = %+v, %v", result, err)
	}
	want := [][]string{
		{"-c", "/t", "query", "-e", "%a == 0", "%n"},
		{"-c", "/t", "query", "-e", "%a == 1 && %#r == 0", "%n"},
		{"-c", "/t", "set", "-y", "-A", "0", "postgresql18-server"},
	}
	for i, cmd := range runner.ran {
		if cmd.Name != "pkg" || !reflect.DeepEqual(cmd.Args, want[i]) || cmd.Timeout == 0 {
			t.Errorf("command %d = %s %q (timeout %v), want pkg %q", i, cmd.Name, cmd.Args, cmd.Timeout, want[i])
		}
	}
}
