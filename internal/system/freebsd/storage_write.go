package freebsd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/storage"
	"github.com/ottermq/jaildeck/internal/system"
)

// StorageAdapter is the ZFS write side. It exists as its own type only because
// host.HostSystem already has a CreateDataset with a different return type, and
// one Go type cannot have both: embedding *Adapter promotes the reads this port
// also needs (Pools, DatasetsUnder, LocalProperties) while the method declared
// here shadows the host one. That is the same duplication JD-022 records, now
// visible in the type system rather than only in a comment.
type StorageAdapter struct {
	*Adapter
}

func NewStorageAdapter(runner system.CommandRunner) *StorageAdapter {
	return &StorageAdapter{Adapter: NewAdapter(runner)}
}

// CreateDataset runs zfs create. Properties become -o options in name order, so
// the same request always produces the same command -- which is what makes the
// argument array assertable in a test and the audit line comparable between two
// runs.
func (a *StorageAdapter) CreateDataset(ctx context.Context, name string, properties map[string]string) (storage.Result, error) {
	args := append([]string{"create"}, propertyArgs(properties, "-o")...)
	return a.runWrite(ctx, name, system.Command{Name: "zfs", Args: append(args, name)})
}

// SetProperties runs one zfs set for all of them. zfs takes any number of
// property=value arguments, so a client setting three tags costs one command
// and writes one audit line.
func (a *StorageAdapter) SetProperties(ctx context.Context, name string, properties map[string]string) (storage.Result, error) {
	args := append([]string{"set"}, propertyArgs(properties, "")...)
	return a.runWrite(ctx, name, system.Command{Name: "zfs", Args: append(args, name)})
}

// InheritProperty runs zfs inherit, one property per call because that is zfs's
// own shape: unlike set, inherit takes a single property name.
func (a *StorageAdapter) InheritProperty(ctx context.Context, name, property string) (storage.Result, error) {
	return a.runWrite(ctx, name, system.Command{Name: "zfs", Args: []string{"inherit", property, name}})
}

func (a *StorageAdapter) runWrite(ctx context.Context, name string, cmd system.Command) (storage.Result, error) {
	_, err := a.runHost(ctx, cmd)
	// The command is returned even when it failed: a failed write is exactly
	// what an operator most wants to see spelled out in the audit log.
	return storage.Result{Dataset: name, Command: cmd.String()}, err
}

// propertyArgs renders property=value arguments in name order. flag is "-o" for
// zfs create and empty for zfs set, which takes them bare.
func propertyArgs(properties map[string]string, flag string) []string {
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)

	args := make([]string, 0, 2*len(names))
	for _, name := range names {
		if flag != "" {
			args = append(args, flag)
		}
		args = append(args, fmt.Sprintf("%s=%s", name, properties[name]))
	}
	return args
}

var _ storage.StorageSystem = (*StorageAdapter)(nil)

// RenameDataset always passes -u. Without it, zfs unmounts the dataset and
// remounts it at the new mountpoint inside the same command -- which, on a
// running jail, is the filesystem vanishing from under a live process. Proven
// on the deployment target: a plain rename of a running jail's data dataset
// left `ls /data` inside the jail answering "No such file or directory".
func (a *StorageAdapter) RenameDataset(ctx context.Context, from, to string) (storage.Result, error) {
	return a.runWrite(ctx, to, system.Command{Name: "zfs", Args: []string{"rename", "-u", from, to}})
}

// Mounted reads the property rather than parsing mount(8): one value, and the
// same question the two calls below need answered before they can succeed.
func (a *StorageAdapter) Mounted(ctx context.Context, dataset string) (bool, error) {
	result, err := a.runHost(ctx, system.Command{
		Name: "zfs", Args: []string{"get", "-H", "-o", "value", "mounted", dataset},
	})
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(result.Stdout) == "yes", nil
}

func (a *StorageAdapter) Unmount(ctx context.Context, dataset string) (storage.Result, error) {
	return a.runWrite(ctx, dataset, system.Command{Name: "zfs", Args: []string{"unmount", dataset}})
}

func (a *StorageAdapter) Mount(ctx context.Context, dataset string) (storage.Result, error) {
	return a.runWrite(ctx, dataset, system.Command{Name: "zfs", Args: []string{"mount", dataset}})
}

func (a *StorageAdapter) CloneSnapshot(ctx context.Context, snapshot, target string) (storage.Result, error) {
	return a.runWrite(ctx, target, system.Command{Name: "zfs", Args: []string{"clone", snapshot, target}})
}

// Origin reads the clone's parent snapshot. zfs prints a dash for a dataset
// that was not cloned, which is an answer rather than an error: a jail whose
// root was created directly has no origin and can still be re-cloned.
func (a *StorageAdapter) Origin(ctx context.Context, dataset string) (string, error) {
	result, err := a.runHost(ctx, system.Command{
		Name: "zfs", Args: []string{"get", "-H", "-o", "value", "origin", dataset},
	})
	if err != nil {
		return "", err
	}
	origin := strings.TrimSpace(result.Stdout)
	if origin == "-" {
		return "", nil
	}
	return origin, nil
}
