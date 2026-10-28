package freebsd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/system"
)

// The two batched reads of the storage domain. Both are written exactly as
// testdata/hostfacts/capture.sh captures them, so every parser test below runs
// on bytes a real host printed rather than on bytes this package invented.
//
// -p is the whole reason the space columns are usable: without it zfs list
// prints "1.41G" and "897G", which no client can total.
var (
	zfsListSpace = []string{"list", "-H", "-p", "-o", "name,used,avail,refer,mountpoint", "-r"}
	// -t filesystem,volume keeps a recursive get out of the snapshots, which
	// it otherwise walks one by one though none has a local property worth
	// listing: 2.52 s against 0.03 s on a host with 381 snapshots under its
	// jails, for the same 10 lines (#135).
	zfsGetLocal = []string{"get", "-H", "-r", "-t", "filesystem,volume", "-o", "name,property,value,source", "-s", "local", "all"}
)

// DatasetsUnder lists each root and everything beneath it in one call.
func (a *Adapter) DatasetsUnder(ctx context.Context, roots []string) ([]storage.Dataset, error) {
	result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: append(slicesClone(zfsListSpace), roots...)})
	if err != nil {
		return nil, err
	}
	return parseZFSListSpace(result.Stdout)
}

// LocalProperties returns every locally-set property of those datasets, native
// ones included: which of them are worth showing is the service's rule.
func (a *Adapter) LocalProperties(ctx context.Context, roots []string) ([]storage.Property, error) {
	result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: append(slicesClone(zfsGetLocal), roots...)})
	if err != nil {
		return nil, err
	}
	return parseZFSGetLocal(result.Stdout)
}

// parseZFSListSpace reads -H -p output: one dataset per line, five tab-separated
// fields.
func parseZFSListSpace(stdout string) ([]storage.Dataset, error) {
	lines := nonEmptyLines(stdout)
	datasets := make([]storage.Dataset, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("parse zfs list: want 5 fields, got %d in %q", len(fields), line)
		}
		used, err := parseZFSSize(fields[1], line)
		if err != nil {
			return nil, err
		}
		available, err := parseZFSSize(fields[2], line)
		if err != nil {
			return nil, err
		}
		referenced, err := parseZFSSize(fields[3], line)
		if err != nil {
			return nil, err
		}
		datasets = append(datasets, storage.Dataset{
			Name:       fields[0],
			Used:       used,
			Available:  available,
			Referenced: referenced,
			Mountpoint: fields[4],
		})
	}
	return datasets, nil
}

// parseZFSSize reads one -p space column. A dash is zfs for "this type does not
// report that figure" -- captured on a snapshot's mountpoint, and the shape a
// zvol's available column takes, which no fixture here covers. Treating it as 0
// keeps one such dataset from failing the whole listing; anything else that is
// not a number is a format change and must be loud.
func parseZFSSize(field, line string) (int64, error) {
	if field == "-" {
		return 0, nil
	}
	n, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse zfs list: %q is not a byte count in %q (is -p missing?)", field, line)
	}
	return n, nil
}

// parseZFSGetLocal reads -H output of zfs get: name, property, value, source.
//
// The value is taken as everything between the property and the source rather
// than as one field, because a user property's value is whatever the operator
// set and is the only column here that can contain a tab.
func parseZFSGetLocal(stdout string) ([]storage.Property, error) {
	lines := nonEmptyLines(stdout)
	properties := make([]storage.Property, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			return nil, fmt.Errorf("parse zfs get: want 4 fields, got %d in %q", len(fields), line)
		}
		// -s local is what makes the source column redundant, and checking it
		// is what makes the column worth asking for: if this ever fails, the
		// filter was dropped and these are not all locally-set properties.
		if source := fields[len(fields)-1]; source != "local" {
			return nil, fmt.Errorf("parse zfs get: source is %q, not local, in %q", source, line)
		}
		properties = append(properties, storage.Property{
			Dataset: fields[0],
			Name:    fields[1],
			Value:   strings.Join(fields[2:len(fields)-1], "\t"),
		})
	}
	return properties, nil
}

// slicesClone keeps the package-level argument prefixes immutable: append to a
// shared slice with spare capacity would overwrite the next caller's roots.
func slicesClone(args []string) []string {
	return append(make([]string, 0, len(args)), args...)
}
