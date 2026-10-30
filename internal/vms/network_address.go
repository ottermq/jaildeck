package vms

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/ottermq/jaildeck/internal/common"
)

// OwnNetwork is one of the networks Jail Deck made, as a guest's NIC joins
// it (JD-047): the default network or a tenant network.
type OwnNetwork struct {
	Name    string
	Switch  string
	Subnet  netip.Prefix
	Gateway netip.Addr
}

// NetworkAddresses is what gives a guest its address on a Jail Deck network.
type NetworkAddresses interface {
	// FindNetwork is the network whose switch is sw. ok is false for a
	// switch that is not one of Jail Deck's: an operator's, whose guests
	// Jail Deck does not address.
	FindNetwork(ctx context.Context, sw string) (OwnNetwork, bool, error)
	// ReserveAddress picks want, when it is a free address of the network,
	// or the lowest free one when want is not set. No other reservation on
	// the network runs until release.
	ReserveAddress(ctx context.Context, n OwnNetwork, want netip.Addr) (address netip.Prefix, release func(), err error)
}

// SetNetworks gives the service Jail Deck's networks, so a guest on one is
// given its address (JD-047).
func (s *Service) SetNetworks(n NetworkAddresses) { s.networks = n }

// placeOnNetwork gives a guest on a Jail Deck network its address: the one
// the request asks for when it is free, the lowest free one otherwise, and
// the network's gateway unless the request gives one. None of Jail Deck's
// networks has a DHCP server, so a guest on one with no address asked for
// is given one rather than left to DHCP. release is called once the seed,
// which is where the next reservation reads the address, is written.
func (s *Service) placeOnNetwork(ctx context.Context, req CreateRequest, spec *CreateSpec) (string, func(), error) {
	none := func() {}
	asksToPick := req.Network != nil && req.Network.Address == ""
	n, ok := OwnNetwork{}, false
	if req.Switch != "" && s.networks != nil {
		var err error
		if n, ok, err = s.networks.FindNetwork(ctx, req.Switch); err != nil {
			return "", none, err
		}
	}
	if !ok {
		if asksToPick {
			return "", none, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"network.address is not set, and %s is not one of Jail Deck's networks, whose addresses it gives; "+
					"give the guest's address with its prefix", strconv.Quote(req.Switch)))
		}
		return "", none, nil
	}
	var want netip.Addr
	if spec.Address != "" {
		p := netip.MustParsePrefix(spec.Address)
		if p.Bits() != n.Subnet.Bits() {
			return "", none, common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
				"network.address is %s, and the network %s is a /%d", p, strconv.Quote(n.Name), n.Subnet.Bits()))
		}
		want = p.Addr()
	}
	address, release, err := s.networks.ReserveAddress(ctx, n, want)
	if err != nil {
		return "", none, err
	}
	spec.Address = address.String()
	if spec.Gateway == "" {
		spec.Gateway = n.Gateway.String()
	}
	return n.Name, release, nil
}

// placeExisting is placeOnNetwork for a guest that is already there. The
// address a request leaves to the network is the guest's own when it is one
// of the subnet's: any free address would have done, and this one was. The
// gateway is the network's, as a create would give. Nothing is reserved.
func (s *Service) placeExisting(ctx context.Context, req CreateRequest, spec *CreateSpec, have Applied) ([]Difference, error) {
	if req.Switch == "" || s.networks == nil {
		return nil, nil
	}
	n, ok, err := s.networks.FindNetwork(ctx, req.Switch)
	if err != nil || !ok {
		return nil, err
	}
	var differences []Difference
	if spec.Address == "" {
		p, err := netip.ParsePrefix(have.Address)
		if err == nil && n.Subnet.Contains(p.Addr()) {
			spec.Address = have.Address
		} else {
			differences = append(differences, Difference{Field: "network.address",
				Want: "an address of " + n.Subnet.String(), Have: have.Address})
			spec.Address = have.Address
		}
	}
	if spec.Gateway == "" {
		spec.Gateway = n.Gateway.String()
	}
	return differences, nil
}
