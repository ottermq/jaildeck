package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

var ErrNotConfigured = errors.New("state dir not configured")

const fileName = "state.json"
const tmpPattern = ".state-*-.tmp"

var createTemp = os.CreateTemp

type State struct {
	PasswordHash string  `json:"password_hash,omitempty"`
	APITokens    []Token `json:"api_tokens,omitempty"`
}

type Token string

type Store struct {
	dir string
}

func NewStore(dir string) *Store {
	return &Store{
		dir: dir,
	}
}

func (s *Store) Dir() string {
	return s.dir
}

// Load reads the state from the store's directory. If the state file does not
// exist, it returns ErrNotConfigured. If the state file is corrupt or cannot
// be read, it returns an error.
func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, fileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, ErrNotConfigured
		}
		return State{}, fmt.Errorf("reading state file: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decoding state file %s: %w", filepath.Join(s.dir, fileName), err)
	}
	return state, nil
}

func (s *Store) Save(st State) error {
	err := os.MkdirAll(s.dir, 0o700)
	if err != nil {
		return fmt.Errorf("failed creating state dir: %w", err)
	}

	tmpFile, err := createTemp(s.dir, tmpPattern)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		if err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err = tmpFile.Chmod(0o600); err != nil {
		return fmt.Errorf("setting permission on temp file: %w", err)
	}

	encoder := json.NewEncoder(tmpFile)
	if err = encoder.Encode(st); err != nil {
		return fmt.Errorf("failed encoding JSON: %w", err)
	}

	if err = tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err = tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	finalPath := filepath.Join(s.dir, fileName)
	if err = os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("failed to rename temp file to state file: %w", err)
	}

	dirFile, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("failed to open dir for syncing: %w", err)
	}

	defer dirFile.Close()

	if err = dirFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}

	return nil
}
