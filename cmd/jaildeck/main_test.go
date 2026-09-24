package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"codeberg.org/Otter-Labs/jaildeck/internal/buildinfo"
)

func TestVersionWithoutCredential(t *testing.T) {
	if os.Getenv("JAILDECK_TEST_VERSION_HELPER") == "1" {
		os.Args = []string{"jaildeck", "version"}
		main()
		os.Exit(0)
	}

	stateDir := filepath.Join(t.TempDir(), "missing-state")
	t.Setenv("JAILDECK_STATE_DIR", stateDir)
	t.Setenv("JAILDECK_TEST_VERSION_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestVersionWithoutCredential$")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version without credential: %v\n%s", err, output)
	}
	if want := buildinfo.Version() + "\n"; string(output) != want {
		t.Errorf("version output = %q, want %q", output, want)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("state directory after version: error = %v, want not exist", err)
	}
}

func TestNewServer_TimeoutsAreSet(t *testing.T) {
	srv := newServer("127.0.0.1:0", http.NewServeMux())

	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout is unset; it is what bounds slow-header clients")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout is unset")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout is unset")
	}
}

func TestNewServer_WriteTimeoutStaysUnset(t *testing.T) {
	srv := newServer("127.0.0.1:0", http.NewServeMux())

	if srv.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %s, want 0: it covers handler execution, and a "+
			"jail operation may run to the 150s backstop (JD-014)", srv.WriteTimeout)
	}
}
