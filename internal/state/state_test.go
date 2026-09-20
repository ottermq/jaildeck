package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingIsNotConfigured(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		dir  string
	}{
		{
			name: "dir does not exist",
			dir:  filepath.Join(dir, "does-not-exist"),
		},
		{
			name: "dir exists, no state.json",
			dir:  dir,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore(tt.dir)

			_, err := store.Load()
			if !errors.Is(err, ErrNotConfigured) {
				t.Errorf("Load() error = %v, want %v", err, ErrNotConfigured)
			}
		})
	}
}

func TestLoad_CorruptFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("not json"), 0o600); err != nil {
		t.Fatalf("writing corrupt state file: %v", err)
	}

	_, err := store.Load()
	if err == nil {
		t.Fatal("Load() succeeded, want error")
	}
	if errors.Is(err, ErrNotConfigured) {
		t.Errorf("Load() error = %v, want a decode error, not ErrNotConfigured", err)
	}
}

func TestSave_CreatesFileWith0600(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Save(State{PasswordHash: "hash"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	filePath := filepath.Join(dir, fileName)
	fInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stating file: %v", err)
	}
	perm := fInfo.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("Perm() = %#o, want %#o", perm, 0o600)
	}
}

func TestSave_TwiceReplacesContents(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	first := State{PasswordHash: "original", APITokens: []Token{"token"}}
	second := State{PasswordHash: "second"}

	if err := store.Save(first); err != nil {
		t.Fatalf("seeding original state: %v", err)
	}

	if err := store.Save(second); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after 2nd Save: %v", err)
	}
	if got.PasswordHash != second.PasswordHash {
		t.Errorf("PasswordHash = %q, want %q", got.PasswordHash, second.PasswordHash)
	}
	if len(got.APITokens) != 0 {
		t.Errorf("APITokens = %v, want none — Save must replace the document, not merge", got.APITokens)
	}
}

func TestSave_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Save(State{PasswordHash: "original"}); err != nil {
		t.Fatalf("Save() = %v", err)
	}

	assertOnlyStateFile(t, dir)

	original := createTemp
	createTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		f.Close()
		return f, nil
	}
	t.Cleanup(func() { createTemp = original })
	if err := store.Save(State{PasswordHash: "replacement"}); err == nil {
		t.Fatalf("Save() succeeded, want error")
	}
	assertOnlyStateFile(t, dir)
}

func assertOnlyStateFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() = %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if len(names) != 1 || names[0] != fileName {
		t.Fatalf("directory contains %v, want only %q — a temp file was left behind", names, fileName)
	}
}

func TestSave_FailureLeavesExistingFileIntact(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Save(State{PasswordHash: "original"}); err != nil {
		t.Fatalf("seeding state: %v", err)
	}

	original := createTemp
	createTemp = func(string, string) (*os.File, error) {
		return nil, errors.New("simulated failure")
	}
	t.Cleanup(func() { createTemp = original })

	if err := store.Save(State{PasswordHash: "replacement"}); err == nil {
		t.Fatal("Save() succeeded, want error")
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() after failed Save: %v", err)
	}
	if got.PasswordHash != "original" {
		t.Errorf("PasswordHash = %q, want %q - failed Save corrupted it", got.PasswordHash, "original")
	}
}
