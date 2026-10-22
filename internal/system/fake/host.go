package fake

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/system"
)

// HostSystem is an in-memory host whose readiness report is deliberately
// mixed — some checks pass, some fail, one is unknown — so the web interface
// has every state to render without a FreeBSD box. Its jails layout starts
// half made, the ordinary state of a host mid-setup, so ensuring it creates
// something the first time and nothing after.
type HostSystem struct {
	mu       sync.Mutex
	datasets []host.Dataset
}

func NewHostSystem() *HostSystem {
	return &HostSystem{
		datasets: []host.Dataset{
			{Name: "zroot", Mountpoint: "none"},
			{Name: "zroot/jails", Mountpoint: "/usr/local/jails"},
			{Name: "zroot/jails/templates", Mountpoint: "/usr/local/jails/templates"},
		},
	}
}

func (s *HostSystem) Sysctl(ctx context.Context, name string) (string, error) {
	switch name {
	case "kern.racct.enable":
		return "0", nil
	case "hw.machine_arch":
		return "amd64", nil
	default:
		return "", fmt.Errorf("sysctl %s: %w", name, host.ErrUnknownOID)
	}
}

func (s *HostSystem) JailConfDefaults(ctx context.Context) (map[string]string, error) {
	return map[string]string{"exec.timeout": "120", "exec.clean": ""}, nil
}

func (s *HostSystem) Bridges(ctx context.Context) ([]string, error) {
	return []string{"bridge0"}, nil
}

func (s *HostSystem) DevfsRulesets(ctx context.Context) ([]host.DevfsRuleset, error) {
	return []host.DevfsRuleset{
		{Name: "devfsrules_hide_all", Number: 1},
		{Name: "devfsrules_unhide_basic", Number: 2},
		{Name: "devfsrules_unhide_login", Number: 3},
		{Name: "devfsrules_jail", Number: 4},
		{Name: "devfsrules_jail_vnet", Number: 5},
	}, nil
}

// CPUVirtualization reports no VT-x line, as a host whose boot log is gone
// would: the report's unknown.
func (s *HostSystem) CPUVirtualization(ctx context.Context) (host.CPUVirtualization, error) {
	return host.CPUVirtualization{}, nil
}

func (s *HostSystem) PFStatus(ctx context.Context) (host.PFStatus, error) {
	return host.PFStatus{Loaded: true}, nil
}

func (s *HostSystem) Pools(ctx context.Context) ([]string, error) {
	return []string{"zroot"}, nil
}

func (s *HostSystem) Datasets(ctx context.Context, pool string) ([]host.Dataset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	datasets := make([]host.Dataset, 0, len(s.datasets))
	for _, d := range s.datasets {
		if d.Name == pool || strings.HasPrefix(d.Name, pool+"/") {
			datasets = append(datasets, d)
		}
	}
	return datasets, nil
}

// CreateDataset gives a dataset its own mountpoint when asked for one and
// otherwise inherits its parent's, as zfs does.
func (s *HostSystem) CreateDataset(ctx context.Context, name string, props map[string]string) (host.DatasetResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	args := []string{"create"}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, "-o", k+"="+props[k])
	}
	result := host.DatasetResult{
		Dataset: name,
		Command: system.Command{Name: "zfs", Args: append(args, name)}.String(),
	}

	parent := path.Dir(name)
	i := slices.IndexFunc(s.datasets, func(d host.Dataset) bool { return d.Name == parent })
	if i < 0 {
		return result, fmt.Errorf("cannot create '%s': parent does not exist", name)
	}
	if slices.ContainsFunc(s.datasets, func(d host.Dataset) bool { return d.Name == name }) {
		return result, fmt.Errorf("cannot create '%s': dataset already exists", name)
	}
	mountpoint, ok := props["mountpoint"]
	if !ok {
		mountpoint = s.datasets[i].Mountpoint
		if mountpoint != "none" {
			mountpoint = path.Join(mountpoint, path.Base(name))
		}
	}
	s.datasets = append(s.datasets, host.Dataset{Name: name, Mountpoint: mountpoint})
	return result, nil
}
