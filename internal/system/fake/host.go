package fake

import (
	"context"
	"fmt"

	"github.com/ottermq/jaildeck/internal/host"
)

// HostSystem is an in-memory host whose readiness report is deliberately
// mixed — some checks pass, some fail, one is unknown — so the web interface
// has every state to render without a FreeBSD box.
type HostSystem struct{}

func NewHostSystem() *HostSystem {
	return &HostSystem{}
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
