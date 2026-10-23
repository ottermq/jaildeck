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
