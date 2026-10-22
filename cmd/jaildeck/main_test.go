package main

import (
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/buildinfo"
	"github.com/ottermq/jaildeck/internal/config"
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

func TestMainLogsBindWarning(t *testing.T) {
	if os.Getenv("JAILDECK_TEST_BIND_WARNING_HELPER") == "1" {
		os.Args = []string{"jaildeck"}
		main()
		os.Exit(0)
	}

	const host = "0.0.0.0"
	t.Setenv("JAILDECK_HOST", host)
	t.Setenv("JAILDECK_STATE_DIR", filepath.Join(t.TempDir(), "missing-state"))
	t.Setenv("JAILDECK_TEST_BIND_WARNING_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestMainLogsBindWarning$")
	output, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("startup without credential: %v, want exit code 1\n%s", err, output)
	}
	if want := config.BindWarning(host); want == "" || !strings.Contains(string(output), want) {
		t.Errorf("startup output = %q, want bind warning %q", output, want)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input   string
		want    slog.Level
		wantErr bool
	}{
		{input: "DEBUG", want: slog.LevelDebug},
		{input: "INFO", want: slog.LevelInfo},
		{input: "WARN", want: slog.LevelWarn},
		{input: "ERROR", want: slog.LevelError},
		{input: "debug", want: slog.LevelDebug},
		{input: "WaRn", want: slog.LevelWarn},
		{input: "INFO+2", want: slog.LevelInfo + 2},
		{input: "ERROR-2", want: slog.LevelError - 2},
		// slog names only four levels. "warning", "trace" and "fatal" are the
		// spellings ottermq's own parser accepts, so they are the likeliest
		// typo when moving between the two projects.
		{input: "warning", want: slog.LevelInfo, wantErr: true},
		{input: "trace", want: slog.LevelInfo, wantErr: true},
		{input: "fatal", want: slog.LevelInfo, wantErr: true},
		{input: "", want: slog.LevelInfo, wantErr: true},
		{input: "invalid", want: slog.LevelInfo, wantErr: true},
		{input: "DEBUG+invalid", want: slog.LevelInfo, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseLogLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseLogLevel(%q) level = %v, want %v", tt.input, got, tt.want)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLogLevel(%q) error = %v, want error: %t", tt.input, err, tt.wantErr)
			}
		})
	}
}
