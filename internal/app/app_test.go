package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"codeberg.org/Otter-Labs/jaildeck/internal/audit"
)

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
