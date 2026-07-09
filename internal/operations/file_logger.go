package operations

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"sync"
)

type FileLogger struct {
	path string
	mu   sync.Mutex
}

func NewFileLogger(path string) *FileLogger {
	return &FileLogger{
		path: path,
	}
}

func (l *FileLogger) Log(ctx context.Context, entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	_, err = file.Write((append(line, '\n')))
	return err
}

func (l *FileLogger) Recent(ctx context.Context, limit int, filter Filter) ([]Entry, error) {
	file, err := os.Open(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []Entry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry Entry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if !applyFilters(entry, filter) {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}

	slices.Reverse(entries)
	return entries, nil
}

func applyFilters(entry Entry, filter Filter) bool {
	if filter.Operation != "" && entry.Operation != filter.Operation {
		return false
	}
	if filter.Success != nil && entry.Success != *filter.Success {
		return false
	}
	if filter.Targets != nil && !slices.Contains(filter.Targets, entry.Target) {
		return false
	}
	return true
}
