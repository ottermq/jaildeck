package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/state"
)

func TestNew_RequiresCredential(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Adapter:  config.AdapterFake,
		StateDir: filepath.Join(root, "state"),
		AuditLog: filepath.Join(root, "log", "audit.log"),
	}

	a, err := New(cfg)
	if a != nil {
		t.Errorf("New() app = %v, want nil", a)
	}
	if !errors.Is(err, ErrNoCredential) {
		t.Fatalf("New() error = %v, want ErrNoCredential", err)
	}
	// The credential check must run before anything touches the filesystem.
	if _, err := os.Stat(filepath.Dir(cfg.AuditLog)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("audit log directory exists after a failed New(); the credential check must run first")
	}
}

func TestNew_SucceedsWithCredential(t *testing.T) {
	a, err := newTestApp(t)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if a == nil {
		t.Fatal("New() app = nil, want non-nil")
	}
}

func newTestApp(t *testing.T) (*App, error) {
	root := t.TempDir()
	cfg := &config.Config{
		Adapter:  config.AdapterFake,
		StateDir: filepath.Join(root, "state"),
		AuditLog: filepath.Join(root, "log", "audit.log"),
	}
	hash, err := auth.Hash("test-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := state.NewStore(cfg.StateDir).Save(state.State{PasswordHash: hash}); err != nil {
		t.Fatalf("seeding credential: %v", err)
	}

	return New(cfg)
}

func TestSetupAuditLog(t *testing.T) {
	t.Run("creates directories and usable log", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "logs", "audit", "audit.log")
		logger, err := setupAuditLog(path)
		if err != nil {
			t.Fatalf("setupAuditLog() error = %v", err)
		}
		if logger == nil {
			t.Fatal("setupAuditLog() returned nil logger")
		}

		for _, dir := range []string{filepath.Join(root, "logs"), filepath.Dir(path)} {
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() || info.Mode().Perm() != 0o700 {
				t.Errorf("directory %q mode = %v, want directory with permissions 0700", dir, info.Mode())
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 0 {
			t.Errorf("log mode = %v, size = %d, want empty regular file with permissions 0600", info.Mode(), info.Size())
		}

		entry := audit.Entry{Operation: "start", Target: "test-jail", Success: true}
		if err := logger.Log(t.Context(), entry); err != nil {
			t.Fatalf("Log() error = %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got audit.Entry
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("decode log entry: %v", err)
		}
		if got != entry {
			t.Errorf("log entry = %+v, want %+v", got, entry)
		}
	})

	t.Run("preserves existing log", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.log")
		const contents = "existing audit entry\n"
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			logger, err := setupAuditLog(path)
			if err != nil || logger == nil {
				t.Fatalf("setupAuditLog() = %v, %v; want non-nil logger and no error", logger, err)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != contents {
			t.Errorf("log contents = %q, want %q", data, contents)
		}
	})

	t.Run("a directory where the log file should be", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "audit.log")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		logger, err := setupAuditLog(path)
		if logger != nil {
			t.Errorf("setupAuditLog() logger = %v, want nil", logger)
		}
		if err == nil {
			t.Fatal("setupAuditLog() returned no error")
		}
		if !strings.HasPrefix(err.Error(), "opening audit log: ") {
			t.Errorf("error = %q, want log opening context", err)
		}
		if !errors.Is(err, syscall.EISDIR) {
			t.Errorf("error = %v, want wrapped EISDIR", err)
		}
	})

	t.Run("returns directory creation error", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(parent, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(parent, "audit.log")
		logger, err := setupAuditLog(path)
		if logger != nil {
			t.Errorf("setupAuditLog() logger = %v, want nil", logger)
		}
		if err == nil {
			t.Fatal("setupAuditLog() returned no error")
		}
		if !strings.HasPrefix(err.Error(), "creating audit log directory: ") {
			t.Errorf("error = %q, want directory creation context", err)
		}
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Errorf("error = %v, want wrapped *os.PathError", err)
		}
	})
	t.Run("existing log file that cannot be written", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("permission checks do not apply to root")
		}
		path := filepath.Join(t.TempDir(), "audit.log")
		if err := os.WriteFile(path, []byte("existing\n"), 0o400); err != nil {
			t.Fatal(err)
		}

		logger, err := setupAuditLog(path)
		if logger != nil {
			t.Errorf("setupAuditLog() logger = %v, want nil", logger)
		}
		if err == nil {
			t.Fatal("setupAuditLog() returned no error")
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("error = %v, want wrapped permission error", err)
		}
	})
}

func TestRequireCredential(t *testing.T) {
	hash, err := auth.Hash("test-password")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := json.Marshal(map[string]string{"password_hash": hash})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name             string
		contents         string
		wantErr          bool
		wantNoCredential bool
	}{
		{name: "missing state dir", wantErr: true, wantNoCredential: true},
		{name: "empty password hash", contents: `{"password_hash":""}`, wantErr: true, wantNoCredential: true},
		{name: "corrupt JSON", contents: `{`, wantErr: true},
		{name: "real credential", contents: string(credential)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDir := filepath.Join(t.TempDir(), "state")
			if tt.contents != "" {
				if err := os.Mkdir(stateDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(tt.contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := requireCredential(state.NewStore(stateDir))
			if (err != nil) != tt.wantErr {
				t.Fatalf("requireCredential() error = %v, want error = %v", err, tt.wantErr)
			}
			if errors.Is(err, ErrNoCredential) != tt.wantNoCredential {
				t.Errorf("requireCredential() error = %v, want ErrNoCredential = %v", err, tt.wantNoCredential)
			}
			if tt.wantNoCredential && !strings.Contains(err.Error(), stateDir) {
				t.Errorf("error = %q, want state directory %q", err, stateDir)
			}
		})
	}
}

func TestPanicIsLoggedThroughSlog(t *testing.T) {
	logs := captureRequestLogs(t)
	r := chi.NewRouter()
	useObservability(r)
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode request log: %v; logs: %s", err, logs.String())
		}
		if record["msg"] == "request panicked" && record["level"] == "ERROR" {
			return
		}
	}
	t.Fatalf("missing request panicked log at ERROR; logs: %s", logs.String())
}

func TestUseObservabilityPutsRequestLoggerInContext(t *testing.T) {
	logs := captureRequestLogs(t)
	r := chi.NewRouter()
	useObservability(r)
	r.Get("/probe", func(_ http.ResponseWriter, req *http.Request) {
		common.Logger(req.Context(), "probe").Info("handler ran")
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log: %v; logs: %s", err, logs.String())
		}
		if record["msg"] != "handler ran" {
			continue
		}
		// The attributes are the assertion. Without the middleware registered,
		// LoggerFromContext falls back to the default logger and this record
		// still appears — just without the request's method and path.
		for key, want := range map[string]any{
			"component": "probe", "method": "GET", "path": "/probe",
		} {
			if got := record[key]; got != want {
				t.Errorf("log %s = %v, want %v", key, got, want)
			}
		}
		return
	}
	t.Fatalf("missing handler log; logs: %s", logs.String())
}
