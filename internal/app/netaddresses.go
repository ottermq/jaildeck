package app

import (
	"context"
	"net/netip"

	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/vms"
)

// jailNetworks and guestNetworks give jails and guests their addresses on
// Jail Deck's own networks (JD-047), from the networks service, so that
// neither domain imports the other.
type jailNetworks struct{ networks *networks.Service }

func (j jailNetworks) FindNetwork(ctx context.Context, name, bridge string) (jails.OwnNetwork, bool, error) {
	n, ok, err := j.networks.Find(ctx, networks.Ref{Name: name, Bridge: bridge})
	if err != nil || !ok {
		return jails.OwnNetwork{}, ok, err
	}
	return jails.OwnNetwork{Name: n.Name, Bridge: n.Bridge, Subnet: n.Subnet, Gateway: n.Gateway}, true, nil
}

func (j jailNetworks) ReserveAddress(ctx context.Context, n jails.OwnNetwork, want netip.Addr) (netip.Prefix, func(), error) {
	return reserve(ctx, j.networks, networks.Ref{Name: n.Name}, want)
}

type guestNetworks struct{ networks *networks.Service }

func (g guestNetworks) FindNetwork(ctx context.Context, sw string) (vms.OwnNetwork, bool, error) {
	n, ok, err := g.networks.Find(ctx, networks.Ref{Switch: sw})
	if err != nil || !ok {
		return vms.OwnNetwork{}, ok, err
	}
	return vms.OwnNetwork{Name: n.Name, Switch: n.Switch, Subnet: n.Subnet, Gateway: n.Gateway}, true, nil
}

func (g guestNetworks) ReserveAddress(ctx context.Context, n vms.OwnNetwork, want netip.Addr) (netip.Prefix, func(), error) {
	return reserve(ctx, g.networks, networks.Ref{Name: n.Name}, want)
}

// reserve finds the network again by name, so the reservation is made
// against the network as the host has it now.
func reserve(ctx context.Context, s *networks.Service, ref networks.Ref, want netip.Addr) (netip.Prefix, func(), error) {
	n, _, err := s.Find(ctx, ref)
	if err != nil {
		return netip.Prefix{}, nil, err
	}
	lease, release, err := s.Reserve(ctx, n, want)
	if err != nil {
		return netip.Prefix{}, nil, err
	}
	return lease.Address, release, nil
}
