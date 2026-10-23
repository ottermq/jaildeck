package freebsd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/system"
)

// zfsListSnapshots is written exactly as testdata/hostfacts/capture.sh captures
// it. -p is what makes creation a Unix timestamp instead of a locale date --
// "Wed Oct 21  0:44 2026", with two spaces before a single-digit hour -- and
// what makes the sizes bytes.
//
// createtxg is the last column and the important one: creation has
// second granularity, so two snapshots taken in the same second cannot be
// ordered by it, and ordering is the whole question a rollback asks.
var zfsListSnapshots = []string{"list", "-H", "-p", "-t", "snapshot", "-o", "name,used,refer,creation,createtxg", "-r"}

func (a *StorageAdapter) Snapshots(ctx context.Context, roots []string) ([]storage.Snapshot, error) {
	result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: append(slicesClone(zfsListSnapshots), roots...)})
	if err != nil {
		return nil, err
	}
	return parseZFSSnapshots(result.Stdout)
}

func (a *StorageAdapter) CreateSnapshot(ctx context.Context, dataset, snapshot string, recursive bool) (storage.Result, error) {
	args := []string{"snapshot"}
	if recursive {
		args = append(args, "-r")
	}
	name := dataset + "@" + snapshot
	return a.runWrite(ctx, name, system.Command{Name: "zfs", Args: append(args, name)})
}

// DestroySnapshot uses -r, never -R. -r is the same snapshot on every
// descendant; -R would take dependent clones with it, which is exactly what
// this refuses to do without being asked (JD-024).
func (a *StorageAdapter) DestroySnapshot(ctx context.Context, dataset, snapshot string, recursive bool) (storage.Result, error) {
	args := []string{"destroy"}
	if recursive {
		args = append(args, "-r")
	}
	name := dataset + "@" + snapshot
	return a.runWrite(ctx, name, system.Command{Name: "zfs", Args: append(args, name)})
}

// Rollback's -r means "destroy the snapshots newer than this one". It is not
// the -r of the two above: zfs rollback has no way to descend into children.
func (a *StorageAdapter) Rollback(ctx context.Context, dataset, snapshot string, destroyNewer bool) (storage.Result, error) {
	args := []string{"rollback"}
	if destroyNewer {
		args = append(args, "-r")
	}
	name := dataset + "@" + snapshot
	return a.runWrite(ctx, name, system.Command{Name: "zfs", Args: append(args, name)})
}

// Clones reads the clones property rather than running a destroy and reading
// its complaint. zfs prints a dash when there are none and a comma-separated
// list when there are.
func (a *StorageAdapter) Clones(ctx context.Context, snapshot string) ([]string, error) {
	result, err := a.runHost(ctx, system.Command{
		Name: "zfs", Args: []string{"get", "-H", "-o", "value", "clones", snapshot},
	})
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(result.Stdout)
	if value == "" || value == "-" {
		return nil, nil
	}
	return strings.Split(value, ","), nil
}

// Holds runs zfs holds, whose -H output is name, tag and timestamp. The
// timestamp is dropped: what the refusal has to say is who is holding it.
func (a *StorageAdapter) Holds(ctx context.Context, snapshot string) ([]storage.Hold, error) {
	result, err := a.runHost(ctx, system.Command{
		Name: "zfs", Args: []string{"holds", "-H", snapshot},
	})
	if err != nil {
		return nil, err
	}
	lines := nonEmptyLines(result.Stdout)
	holds := make([]storage.Hold, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			return nil, fmt.Errorf("parse zfs holds: want at least 2 fields, got %d in %q", len(fields), line)
		}
		holds = append(holds, storage.Hold{Snapshot: fields[0], Tag: fields[1]})
	}
	return holds, nil
}

// parseZFSSnapshots reads -H -p output: five tab-separated fields per line.
func parseZFSSnapshots(stdout string) ([]storage.Snapshot, error) {
	lines := nonEmptyLines(stdout)
	snapshots := make([]storage.Snapshot, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("parse zfs list -t snapshot: want 5 fields, got %d in %q", len(fields), line)
		}
		dataset, short, ok := strings.Cut(fields[0], "@")
		if !ok {
			return nil, fmt.Errorf("parse zfs list -t snapshot: %q is not a snapshot name", fields[0])
		}
		used, err := parseZFSSize(fields[1], line)
		if err != nil {
			return nil, err
		}
		referenced, err := parseZFSSize(fields[2], line)
		if err != nil {
			return nil, err
		}
		created, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse zfs list -t snapshot: %q is not a Unix timestamp in %q (is -p missing?)", fields[3], line)
		}
		createTXG, err := strconv.ParseUint(fields[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse zfs list -t snapshot: %q is not a transaction group in %q", fields[4], line)
		}
		snapshots = append(snapshots, storage.Snapshot{
			Name:       fields[0],
			Dataset:    dataset,
			Short:      short,
			Used:       used,
			Referenced: referenced,
			Created:    time.Unix(created, 0).UTC(),
			CreateTXG:  createTXG,
		})
	}
	return snapshots, nil
}
