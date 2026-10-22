package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/system/fake"
)

func initConfig(t *testing.T) *config.Config {
	return &config.Config{
		Adapter:  config.AdapterFake,
		AuditLog: filepath.Join(t.TempDir(), "log", "audit.log"),
	}
}

// The fake host starts with zroot/jails and its templates child, so init has
// two datasets to create — and needs no operator credential to do it.
func TestRunInit(t *testing.T) {
	cfg := initConfig(t)
	var out, errOut bytes.Buffer
	if err := runInit(nil, &out, &errOut, cfg); err != nil {
		t.Fatalf("runInit() = %v", err)
	}
	want := "created zroot/jails/media\ncreated zroot/jails/containers\nzroot/jails is in place\n"
	if out.String() != want || errOut.Len() != 0 {
		t.Errorf("out = %q, errOut = %q; want %q", out.String(), errOut.String(), want)
	}
	entries, err := audit.NewFileLogger(cfg.AuditLog).Recent(context.Background(), 0, audit.Filter{Operation: "create-dataset"})
	if err != nil || len(entries) != 2 {
		t.Errorf("audit entries = %v, %v; want two create-dataset entries", entries, err)
	}
}

func TestEnsureLayoutTwiceChangesNothing(t *testing.T) {
	service := host.NewService(fake.NewHostSystem(), audit.NewFileLogger(filepath.Join(t.TempDir(), "audit.log")), "")
	if err := ensureLayout(context.Background(), service, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := ensureLayout(context.Background(), service, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if want := "zroot/jails is already in place: nothing changed\n"; out.String() != want {
		t.Errorf("second run printed %q, want %q", out.String(), want)
	}
}

func TestRunInitRejects(t *testing.T) {
	if err := runInit([]string{"tank"}, io.Discard, io.Discard, initConfig(t)); !errors.Is(err, ErrUnexpectedArgs) {
		t.Errorf("runInit(tank) = %v, want ErrUnexpectedArgs", err)
	}
	cfg := initConfig(t)
	cfg.Adapter = "linux"
	if err := runInit(nil, io.Discard, io.Discard, cfg); err == nil {
		t.Error("runInit() with an unknown adapter succeeded")
	}
}
