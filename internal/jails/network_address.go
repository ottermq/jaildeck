package jails

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"

	"github.com/ottermq/jaildeck/internal/common"
)

// OwnNetwork is one of the networks Jail Deck made, as a VNET jail joins it
// (JD-047): the default network or a tenant network.
type OwnNetwork struct {
	Name    string
	Bridge  string
	Subnet  netip.Prefix
	Gateway netip.Addr
}

// NetworkAddresses is what gives a jail its address on a Jail Deck network.
type NetworkAddresses interface {
	// FindNetwork is the network named name, or whose bridge is bridge. ok
	// is false for a bridge that is not one of Jail Deck's: an operator's,
	// whose jails Jail Deck does not address. A name that is no network is
	// an error.
	FindNetwork(ctx context.Context, name, bridge string) (OwnNetwork, bool, error)
	// ReserveAddress picks want, when it is a free address of the network,
	// or the lowest free one when want is not set. No other reservation on
	// the network runs until release.
	ReserveAddress(ctx context.Context, n OwnNetwork, want netip.Addr) (address netip.Prefix, release func(), err error)
}

// placeOnNetwork puts a VNET jail on the Jail Deck network its spec names,
// by name or by bridge: the bridge is the network's, the address is reserved
// and, when the spec has none, picked, and the gateway is the network's
// unless the spec gives one. A jail on an operator's bridge is left as its
// spec says. release is called once the stanza is written.
func (s *JailService) placeOnNetwork(ctx context.Context, spec *Spec) (release func(), err error) {
	none := func() {}
	if spec.Network.Mode != NetworkVNET {
		return none, nil
	}
	if s.networks == nil {
		if spec.Network.Name != "" {
			return none, common.NewAppError(http.StatusNotImplemented,
				"this Jail Deck cannot read its networks, so a jail cannot be put on one by name; give network.bridge")
		}
		return none, nil
	}
	n, ok, err := s.networks.FindNetwork(ctx, spec.Network.Name, spec.Network.Bridge)
	if err != nil || !ok {
		return none, err
	}
	spec.Network.Bridge = n.Bridge
	var want netip.Addr
	if spec.Network.IP4 != "" {
		p, _ := ip4Prefix(spec.Network.IP4)
		if p.Bits() != n.Subnet.Bits() && p.Bits() != 32 {
			return none, badSpec(fmt.Sprintf("network.ip4 is %s, and the network %q is a /%d; give the address alone, or with /%d",
				spec.Network.IP4, n.Name, n.Subnet.Bits(), n.Subnet.Bits()))
		}
		want = p.Addr()
	}
	address, release, err := s.networks.ReserveAddress(ctx, n, want)
	if err != nil {
		return none, err
	}
	spec.Network.IP4 = address.String()
	if spec.Network.Gateway == "" {
		spec.Network.Gateway = n.Gateway.String()
	}
	spec.Network.Name = n.Name
	return release, nil
}

// stanzaAddressRE is the address a stanza Jail Deck wrote gives its jail.
var stanzaAddressRE = regexp.MustCompile(`exec\.start \+= "ifconfig epair[0-9]+b inet (\S+) up";`)

// placeExisting is placeOnNetwork for a jail that is already there. The
// address a spec leaves to the network is the jail's own when it is one of
// the subnet's: any free address would have done, and this one was. The
// gateway is the network's, as a create would give. Nothing is reserved.
func (s *JailService) placeExisting(ctx context.Context, spec *Spec, content string) ([]Difference, error) {
	if spec.Network.Mode != NetworkVNET || s.networks == nil {
		return nil, nil
	}
	n, ok, err := s.networks.FindNetwork(ctx, spec.Network.Name, spec.Network.Bridge)
	if err != nil || !ok {
		return nil, err
	}
	spec.Network.Bridge = n.Bridge
	if spec.Network.IP4 == "" {
		have := ""
		if m := stanzaAddressRE.FindStringSubmatch(content); m != nil {
			have = m[1]
		}
		p, err := netip.ParsePrefix(have)
		if err != nil || !n.Subnet.Contains(p.Addr()) {
			return []Difference{{Field: "network.ip4", Want: "an address of " + n.Subnet.String(), Have: have}}, nil
		}
		spec.Network.IP4 = have
	}
	if spec.Network.Gateway == "" {
		spec.Network.Gateway = n.Gateway.String()
	}
	return nil, nil
}
