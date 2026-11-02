package freebsd

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buffer is a device to write a disk into: zeros until written, as a fresh
// sparse zvol reads.
type buffer struct{ b []byte }

func (w *buffer) WriteAt(p []byte, off int64) (int, error) { return copy(w.b[off:], p), nil }

func qcow2Fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "qcow2", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func convert(t *testing.T, image []byte) ([]byte, int64, error) {
	t.Helper()
	q, err := openQcow2(bytes.NewReader(image))
	if err != nil {
		return nil, 0, err
	}
	out := &buffer{b: make([]byte, q.size)}
	n, err := q.writeTo(context.Background(), out)
	return out.b, n, err
}

// The fixtures are qemu-img's own output (testdata/qcow2/make-fixtures.sh):
// what it wrote, read back here, must be the disk it was given.
func TestQcow2ReadsWhatQemuImgWrote(t *testing.T) {
	raw := qcow2Fixture(t, "disk.raw")
	zeroed := append(make([]byte, 4096), raw[4096:]...)
	for name, want := range map[string][]byte{
		"plain.qcow2":          raw,
		"compressed.qcow2":     raw,
		"v2.qcow2":             raw,
		"compressed-64k.qcow2": raw,
		"zero-flag.qcow2":      zeroed,
	} {
		got, written, err := convert(t, qcow2Fixture(t, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: the disk differs from disk.raw", name)
		}
		// The holes are not written: a sparse zvol already reads as zeros.
		// With 64 KiB clusters this disk is one cluster and has none.
		if name != "compressed-64k.qcow2" && written >= int64(len(want)) {
			t.Errorf("%s: wrote %d of %d bytes, holes included", name, written, len(want))
		}
	}
}

func TestQcow2RefusesWhatItDoesNotRead(t *testing.T) {
	plain := qcow2Fixture(t, "plain.qcow2")
	patch := func(at int, value uint64, width int) []byte {
		b := bytes.Clone(plain)
		if width == 4 {
			binary.BigEndian.PutUint32(b[at:], uint32(value))
		} else {
			binary.BigEndian.PutUint64(b[at:], value)
		}
		return b
	}
	for name, c := range map[string]struct {
		image []byte
		says  string
	}{
		"not qcow2":    {[]byte(strings.Repeat("disk.raw ", 20)), "not a qcow2 image"},
		"version 4":    {patch(4, 4, 4), "version 4"},
		"encrypted":    {patch(32, 1, 4), "encrypted"},
		"backing file": {patch(8, 512, 8), "backing file"},
		"dirty":        {patch(72, 1, 8), "incompatible features 0x1"},
		"zstd":         {patch(72, 1<<3, 8), "incompatible features 0x8"},
		"short":        {plain[:40], "qcow2 header"},
		"no L1":        {patch(36, 0, 4), "L1 table of 0 entries"},
		"cut off":      {plain[:12288], "qcow2"},
	} {
		_, _, err := convert(t, c.image)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: err = %v, want it to say %q", name, err, c.says)
		}
	}
}

func TestQcow2StopsWhenCancelled(t *testing.T) {
	q, err := openQcow2(bytes.NewReader(qcow2Fixture(t, "plain.qcow2")))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.writeTo(ctx, &buffer{b: make([]byte, q.size)}); err != context.Canceled {
		t.Errorf("err = %v", err)
	}
}

// From a file into a device node, as Write does it: a regular file stands
// in for the zvol.
func TestWriteQcow2IntoADevice(t *testing.T) {
	device := filepath.Join(t.TempDir(), "zvol")
	if err := os.WriteFile(device, make([]byte, 41472), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", "qcow2", "compressed.qcow2")
	if size, err := qcow2Size(src); err != nil || size != 41472 {
		t.Fatalf("size = %d, %v", size, err)
	}
	if _, err := writeQcow2(context.Background(), src, device); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(device)
	if !bytes.Equal(got, qcow2Fixture(t, "disk.raw")) {
		t.Error("the device differs from disk.raw")
	}
}
