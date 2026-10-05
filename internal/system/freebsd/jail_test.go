package freebsd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codeberg.org/Otter-Labs/jaildeck/internal/common"
	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
)

func TestParseJLSOutput_Success(t *testing.T) {
	input := `{"__version": "2", "jail-information": {"jail": [{"jid":"1","name":"goodiesdb","host.hostname":"goodiesdb", "ip4.addr": [],"path":"/usr/local/jails/containers/goodiesdb"}]}}`

	got, err := parseJLSOutput(input)
	if err != nil {
		t.Fatalf("parse JLS output: %v", err)
	}
	if len(got.JailInformation.Jails) != 1 {
		t.Fatalf("jail count = %d, want 1", len(got.JailInformation.Jails))
	}

	jail := got.JailInformation.Jails[0]
	if jail.Name != "goodiesdb" {
		t.Fatalf("jail name = %q, want %q", jail.Name, "goodiesdb")
	}

	if jail.JID != "1" {
		t.Fatalf("jid = %q, want %q", jail.JID, "1")
	}
	if jail.Hostname != "goodiesdb" {
		t.Fatalf("hostname = %q, want %q", jail.Hostname, "goodiesdb")
	}
	if jail.Path != "/usr/local/jails/containers/goodiesdb" {
		t.Fatalf("path = %q", jail.Path)
	}
}

func TestSummarizeJailActionFailure(t *testing.T) {
	tests := []struct {
		name   string
		op     jails.Operation
		result system.CommandResult
		want   string
	}{
		{
			name: "stderr takes precedence",
			op:   jails.OperationStart,
			result: system.CommandResult{
				Stderr: "jail: goodiesdb: permission denied",
				Stdout: "different error",
			},
			want: "permission denied",
		},
		{
			name: "blank stderr falls back to stdout",
			op:   jails.OperationStart,
			result: system.CommandResult{
				Stderr: " \n\t",
				Stdout: "Starting jails: cannot start jail \"goodiesdb\": jail: goodiesdb: mount failed.",
			},
			want: "mount failed",
		},
		{
			name:   "whitespace is collapsed before prefixes are stripped",
			op:     jails.OperationStart,
			result: system.CommandResult{Stderr: "cannot start jail  \"goodiesdb\": invalid configuration"},
			want:   "invalid configuration",
		},
		{
			name:   "stop prefix and quoted name",
			op:     jails.OperationStop,
			result: system.CommandResult{Stderr: "Stopping jails: jail: \"goodiesdb\": process still running."},
			want:   "process still running",
		},
		{
			name:   "normalize whitespace and trim punctuation",
			op:     jails.OperationRestart,
			result: system.CommandResult{Stderr: " \n.: first\t error\nsecond   error :.\n"},
			want:   "first error second error",
		},
		{
			name: "empty output",
			op:   jails.OperationStart,
			want: "start jail \"goodiesdb\" failed",
		},
		{
			name:   "only prefixes and punctuation",
			op:     jails.OperationStop,
			result: system.CommandResult{Stdout: "Stopping jails: jail: goodiesdb: ."},
			want:   "stop jail \"goodiesdb\" failed",
		},
		{
			name:   "preserve other jail names",
			op:     jails.OperationRestart,
			result: system.CommandResult{Stderr: "jail: otherdb: unavailable"},
			want:   "jail: otherdb: unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeJailActionFailure("goodiesdb", tt.op, tt.result)
			if got != tt.want {
				t.Errorf("summarizeJailActionFailure() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeJails(t *testing.T) {
	stopped := jails.Jail{
		Name: "database", Status: jails.JailStatusStopped,
		Hostname: "configured-db", IP: "192.0.2.1", Path: "/jails/database",
	}
	running := jails.Jail{
		JID: "7", Name: "database", Status: jails.JailStatusRunning,
		Hostname: "running-db", IP: "192.0.2.2", Path: "/running/database",
	}
	other := jails.Jail{Name: "web", Status: jails.JailStatusStopped}
	unconfigured := jails.Jail{JID: "8", Name: "worker", Status: jails.JailStatusRunning}
	secondUnconfigured := jails.Jail{JID: "9", Name: "cache", Status: jails.JailStatusRunning}

	tests := []struct {
		name         string
		configured   []jails.Jail
		running      []jails.Jail
		want         []jails.Jail
		wantWarnings []string
	}{
		{name: "empty", want: []jails.Jail{}},
		{
			name: "configured only", configured: []jails.Jail{stopped, other},
			want:         []jails.Jail{stopped, other},
			wantWarnings: nil,
		},
		{
			name: "running only", running: []jails.Jail{unconfigured, running},
			want:         []jails.Jail{unconfigured, running},
			wantWarnings: []string{"worker", "database"},
		},
		{
			name: "running replaces configured fields", configured: []jails.Jail{stopped},
			running: []jails.Jail{running}, want: []jails.Jail{running},
			wantWarnings: nil,
		},
		{
			name:         "preserves configured order and appends unconfigured in running order",
			configured:   []jails.Jail{stopped, other},
			running:      []jails.Jail{unconfigured, running, secondUnconfigured},
			want:         []jails.Jail{running, other, unconfigured, secondUnconfigured},
			wantWarnings: []string{"worker", "cache"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureDefaultLogs(t)
			got := mergeJails(tt.configured, tt.running)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeJails() = %#v, want %#v", got, tt.want)
			}
			var gotWarnings []string
			decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
			for decoder.More() {
				var rec map[string]any
				if err := decoder.Decode(&rec); err != nil {
					t.Fatalf("decode log: %v\nlogs: %s", err, logs.String())
				}
				if rec["level"] != "WARN" || rec["msg"] != "jail not found in configuration files" {
					continue
				}
				name, _ := rec["jail"].(string)
				gotWarnings = append(gotWarnings, name)
			}
			if !reflect.DeepEqual(gotWarnings, tt.wantWarnings) {
				t.Errorf("warned about %v, want %v", gotWarnings, tt.wantWarnings)
			}
		})
	}
}

// runnerFunc adapts a function to system.CommandRunner so each test can state
// the single command result it cares about.
type runnerFunc func(ctx context.Context, cmd system.Command) (system.CommandResult, error)

func (f runnerFunc) Run(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
	return f(ctx, cmd)
}

var _ system.CommandRunner = runnerFunc(nil)

func TestRunningJails(t *testing.T) {
	runnerErr := errors.New("jls unavailable")
	tests := []struct {
		name       string
		result     system.CommandResult
		runnerErr  error
		want       []jails.Jail
		wantErr    string
		wantRecord map[string]any
	}{
		{
			name:   "happy path",
			result: system.CommandResult{Stdout: `{"jail-information":{"jail":[{"jid":"7","name":"database","host.hostname":"db.example","ip4.addr":["192.0.2.1","192.0.2.2"],"path":"/jails/database"},{"jid":"8","name":"web","host.hostname":"web.example","ip4.addr":[],"path":"/jails/web"}]}}`},
			want: []jails.Jail{
				{JID: "7", Name: "database", Status: jails.JailStatusRunning, Hostname: "db.example", IP: "192.0.2.1, 192.0.2.2", Path: "/jails/database"},
				{JID: "8", Name: "web", Status: jails.JailStatusRunning, Hostname: "web.example", Path: "/jails/web"},
			},
		},
		{
			name:       "unparseable stdout",
			result:     system.CommandResult{Stdout: "not json", Stderr: "jls diagnostic"},
			wantErr:    "fail parsing jls output: ",
			wantRecord: map[string]any{"level": "DEBUG", "msg": "failed to parse jls output", "stderr": "jls diagnostic"},
		},
		{
			name:   "empty name is logged and returned",
			result: system.CommandResult{Stdout: `{"jail-information":{"jail":[{"jid":"9","name":"","host.hostname":"unnamed","ip4.addr":["192.0.2.3"],"path":"/jails/unnamed"},{"jid":"10","name":"worker"}]}}`},
			want: []jails.Jail{
				{JID: "9", Status: jails.JailStatusRunning, Hostname: "unnamed", IP: "192.0.2.3", Path: "/jails/unnamed"},
				{JID: "10", Name: "worker", Status: jails.JailStatusRunning},
			},
			wantRecord: map[string]any{"level": "WARN", "msg": "jls reported a jail with no name", "jid": "9"},
		},
		{
			name: "runner error", runnerErr: runnerErr,
			wantErr: "list jails: jls unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, logs := captureRequestLogs(t, "method", "GET", "path", "/jails")
			calls := 0
			adapter := NewAdapter(runnerFunc(func(gotCtx context.Context, cmd system.Command) (system.CommandResult, error) {
				calls++
				if gotCtx != ctx {
					t.Error("runner did not receive the request context")
				}
				if !reflect.DeepEqual(cmd, jlsListCommand) {
					t.Errorf("command = %#v, want %#v", cmd, jlsListCommand)
				}
				return tt.result, tt.runnerErr
			}))
			got, err := adapter.runningJails(ctx)
			if calls != 1 {
				t.Errorf("runner calls = %d, want 1", calls)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("runningJails() error = %v", err)
				}
			} else if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Fatalf("runningJails() error = %v, want prefix %q", err, tt.wantErr)
			}
			if tt.runnerErr != nil && (!errors.Is(err, tt.runnerErr) || err.Error() != tt.wantErr) {
				t.Errorf("runner error was not wrapped as expected: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("runningJails() = %#v, want %#v", got, tt.want)
			}
			if tt.wantRecord == nil {
				if logs.Len() != 0 {
					t.Errorf("unexpected request logs: %s", logs.String())
				}
				return
			}
			var record map[string]any
			if decodeErr := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record); decodeErr != nil {
				t.Fatalf("decode request log: %v; logs: %s", decodeErr, logs.String())
			}
			for key, want := range tt.wantRecord {
				if record[key] != want {
					t.Errorf("log %s = %v, want %v", key, record[key], want)
				}
			}
			for key, want := range map[string]string{"component": "freebsd", "method": "GET", "path": "/jails"} {
				if record[key] != want {
					t.Errorf("request log %s = %v, want %q", key, record[key], want)
				}
			}
			if tt.wantRecord["level"] == "DEBUG" {
				var syntaxErr *json.SyntaxError
				if !errors.As(err, &syntaxErr) {
					t.Fatalf("parse error is not wrapped: %v", err)
				}
				if record["err"] != syntaxErr.Error() {
					t.Errorf("log err = %v, want %q", record["err"], syntaxErr.Error())
				}
			}
		})
	}
}

// captureDefaultLogs swaps the global logger; callers must not use t.Parallel.
func captureDefaultLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

func captureRequestLogs(t *testing.T, attrs ...any) (context.Context, *bytes.Buffer) {
	t.Helper()
	captureDefaultLogs(t)
	var requestLogs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&requestLogs, &slog.HandlerOptions{Level: slog.LevelDebug})).With(attrs...)
	return common.ContextWithLogger(context.Background(), logger), &requestLogs
}

func TestAdapterList(t *testing.T) {
	runnerErr := errors.New("jls unavailable")
	stopped := jails.Jail{Name: "web", Hostname: "configured-web", Path: "/from-conf", Status: jails.JailStatusStopped}
	running := jails.Jail{JID: "7", Name: "web", Path: "/jails/web", Status: jails.JailStatusRunning}
	worker := jails.Jail{JID: "8", Name: "worker", Status: jails.JailStatusRunning}
	tests := []struct {
		name         string
		configured   bool
		stdout       string
		runnerErr    error
		configErr    bool
		want         []jails.Jail
		wantCalls    int
		wantWarnings []string
	}{
		{name: "configured only", configured: true, stdout: `{"jail-information":{"jail":[]}}`, want: []jails.Jail{stopped}, wantCalls: 1},
		{name: "running only", stdout: `{"jail-information":{"jail":[{"jid":"7","name":"web","path":"/jails/web"}]}}`, want: []jails.Jail{running}, wantCalls: 1, wantWarnings: []string{"web"}},
		{name: "in both", configured: true, stdout: `{"jail-information":{"jail":[{"jid":"7","name":"web","path":"/jails/web"}]}}`, want: []jails.Jail{running}, wantCalls: 1},
		{name: "partial overlap", configured: true, stdout: `{"jail-information":{"jail":[{"jid":"8","name":"worker"}]}}`, want: []jails.Jail{stopped, worker}, wantCalls: 1, wantWarnings: []string{"worker"}},
		{name: "jls error", runnerErr: runnerErr, wantCalls: 1},
		{name: "configDir error", configErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureDefaultLogs(t)
			dir := t.TempDir()
			if tt.configured {
				if err := os.WriteFile(filepath.Join(dir, "web.conf"), []byte("web {\nhost.hostname = configured-web;\npath = /from-conf;\n}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.configErr {
				if err := os.Mkdir(filepath.Join(dir, "foo.conf"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			calls := 0
			adapter := NewAdapter(runnerFunc(func(gotCtx context.Context, cmd system.Command) (system.CommandResult, error) {
				calls++
				if gotCtx != ctx {
					t.Error("runner did not receive the list context")
				}
				if !reflect.DeepEqual(cmd, jlsListCommand) {
					t.Errorf("command = %#v, want %#v", cmd, jlsListCommand)
				}
				return system.CommandResult{Stdout: tt.stdout}, tt.runnerErr
			}))
			adapter.confDir = dir
			adapter.confFile = filepath.Join(dir, "missing.conf")
			got, err := adapter.List(ctx)
			if calls != tt.wantCalls {
				t.Errorf("runner calls = %d, want %d", calls, tt.wantCalls)
			}
			switch {
			case tt.runnerErr != nil:
				if !errors.Is(err, tt.runnerErr) {
					t.Errorf("List() error = %v, want wrapped %v", err, tt.runnerErr)
				}
			case tt.configErr:
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || pathErr.Path != filepath.Join(dir, "foo.conf") || errors.Is(err, os.ErrNotExist) {
					t.Errorf("List() error = %v, want read error for foo.conf", err)
				}
			default:
				if err != nil {
					t.Fatalf("List() error = %v", err)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("List() = %#v, want %#v", got, tt.want)
			}
			var warnings []string
			decoder := json.NewDecoder(bytes.NewReader(logs.Bytes()))
			for decoder.More() {
				var record map[string]any
				if err := decoder.Decode(&record); err != nil {
					t.Fatalf("decode log: %v", err)
				}
				if record["level"] != "WARN" || record["msg"] != "jail not found in configuration files" {
					t.Errorf("unexpected log: %v", record)
					continue
				}
				name, _ := record["jail"].(string)
				warnings = append(warnings, name)
			}
			if !reflect.DeepEqual(warnings, tt.wantWarnings) {
				t.Errorf("warned about %v, want %v", warnings, tt.wantWarnings)
			}
		})
	}
}

func TestGetJailByName(t *testing.T) {
	runnerErr := errors.New("jls unavailable")
	web := jails.Jail{JID: "7", Name: "web", Hostname: "web.example", IP: "192.0.2.7", Path: "/jails/web", Status: jails.JailStatusRunning}
	webJSON := `{"jid":"7","name":"web","host.hostname":"web.example","ip4.addr":["192.0.2.7"],"path":"/jails/web"}`
	tests := []struct {
		name      string
		entries   string
		lookup    string
		runnerErr error
		want      jails.Jail
		wantErr   error
	}{
		{name: "found", entries: webJSON, lookup: "web", want: web},
		{name: "not found", entries: webJSON, lookup: "missing", wantErr: jails.ErrJailNotFound},
		{name: "List error passes through", lookup: "web", runnerErr: runnerErr, wantErr: runnerErr},
		{name: "found second in list", entries: `{"jid":"8","name":"worker"},` + webJSON, lookup: "web", want: web},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captureDefaultLogs(t)
			adapter := NewAdapter(runnerFunc(func(context.Context, system.Command) (system.CommandResult, error) {
				return system.CommandResult{Stdout: `{"jail-information":{"jail":[` + tt.entries + `]}}`}, tt.runnerErr
			}))
			adapter.confDir = t.TempDir()
			adapter.confFile = filepath.Join(adapter.confDir, "missing.conf")
			got, err := adapter.getJailByName(context.Background(), tt.lookup)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("getJailByName() error = %v, want %v", err, tt.wantErr)
			}
			if tt.runnerErr != nil && err.Error() != "list jails: "+tt.runnerErr.Error() {
				t.Errorf("List error changed: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getJailByName() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
