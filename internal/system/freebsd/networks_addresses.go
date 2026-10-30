package freebsd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/vms"
)

// ipv4Literal is an IPv4 address written anywhere in a file, with or without
// its prefix.
var ipv4Literal = regexp.MustCompile(`(^|[^0-9.])((?:[0-9]{1,3}\.){3}[0-9]{1,3})(/[0-9]{1,2})?([^0-9.]|$)`)

// AddressUses reads every address of subnet that something has, three ways
// (JD-047):
//
//   - every jail configuration, the jail.conf.d files and /etc/jail.conf:
//     any address of the subnet written anywhere in one counts as that
//     jail's, whatever form the stanza has. Jail Deck's own write it as
//     `ifconfig epair<n>b inet <address> up`; one written by hand may not.
//     Taking too much only costs an address;
//   - every guest's cloud-init network-config in the default datastore, the
//     address vm-bhyve gave it;
//   - the bridge's ARP table, `arp -an -i <bridge>`: what has talked on the
//     bridge, an address set by hand inside a jail included. Measured on
//     isengard: an address configured that has sent nothing is not there,
//     and an entry expires after net.link.ether.inet.max_age (1200 s), so it
//     only ever adds to what the configurations say.
func (a *Adapter) AddressUses(ctx context.Context, subnet netip.Prefix, bridge string) ([]networks.Use, error) {
	uses, err := a.jailAddressUses(subnet)
	if err != nil {
		return nil, err
	}
	guests, err := a.guestAddressUses(ctx, subnet)
	if err != nil {
		return nil, err
	}
	uses = append(uses, guests...)
	result, err := a.runHost(ctx, system.Command{Name: "arp", Args: []string{"-an", "-i", bridge}})
	if err != nil {
		return nil, err
	}
	return append(uses, parseARP(result.Stdout, subnet)...), nil
}

func (a *Adapter) jailAddressUses(subnet netip.Prefix) ([]networks.Use, error) {
	files, err := filepath.Glob(filepath.Join(a.confDir, "*.conf"))
	if err != nil {
		return nil, err
	}
	main := strings.TrimSuffix(a.confDir, ".d")
	files = append(files, main)
	uses := make([]networks.Use, 0)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		name := strings.TrimSuffix(filepath.Base(f), ".conf")
		if f == main {
			name = main
		}
		for _, addr := range addressesIn(string(content), subnet) {
			uses = append(uses, networks.Use{Address: addr.String(), Kind: networks.UseJail, Name: name})
		}
	}
	return uses, nil
}

// addressesIn is every address of subnet the text writes, in order.
func addressesIn(text string, subnet netip.Prefix) []netip.Addr {
	found := make([]netip.Addr, 0)
	for _, line := range strings.Split(text, "\n") {
		for _, m := range ipv4Literal.FindAllStringSubmatch(stripComment(line), -1) {
			addr, err := netip.ParseAddr(m[2])
			if err == nil && subnet.Contains(addr) {
				found = append(found, addr)
			}
		}
	}
	return found
}

// guestAddressUses reads the address each guest's seed gives it. A host
// without vm-bhyve has no guests to read; one whose vm-bhyve cannot be read
// is refused, since every network Jail Deck makes has a vm-bhyve switch.
func (a *Adapter) guestAddressUses(ctx context.Context, subnet netip.Prefix) ([]networks.Use, error) {
	dir, _, err := a.vmDatastore(ctx)
	if errors.Is(err, system.ErrCommandNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seeds, err := filepath.Glob(filepath.Join(dir, "*", ".cloud-init", "network-config"))
	if err != nil {
		return nil, err
	}
	uses := make([]networks.Use, 0)
	for _, seed := range seeds {
		content, err := os.ReadFile(seed)
		if err != nil {
			return nil, err
		}
		guest := filepath.Base(filepath.Dir(filepath.Dir(seed)))
		var applied vms.Applied
		if err := parseNetworkConfig(content, &applied); err != nil {
			return nil, fmt.Errorf("reading %s's network-config: %w", guest, err)
		}
		p, err := netip.ParsePrefix(applied.Address)
		if err != nil || !subnet.Contains(p.Addr()) {
			continue
		}
		uses = append(uses, networks.Use{Address: p.Addr().String(), Kind: networks.UseGuest, Name: guest})
	}
	return uses, nil
}

// arpEntry is one line of `arp -an`:
//
//	? (10.64.1.10) at 58:9c:fc:10:f4:91 on jdn1 expires in 628 seconds [bridge]
//	? (10.64.1.1) at 58:9c:fc:10:60:3f on jdn1 permanent [bridge]
//
// A permanent entry is one of the host's own addresses; an incomplete one has
// no MAC and is something asked after that never answered.
var arpEntry = regexp.MustCompile(`^\? \(([0-9.]+)\) at ([0-9a-f:]+) on \S+ (.*)$`)

func parseARP(stdout string, subnet netip.Prefix) []networks.Use {
	uses := make([]networks.Use, 0)
	for _, line := range nonEmptyLines(stdout) {
		m := arpEntry.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || strings.HasPrefix(m[3], "permanent") {
			continue
		}
		addr, err := netip.ParseAddr(m[1])
		if err != nil || !subnet.Contains(addr) {
			continue
		}
		uses = append(uses, networks.Use{Address: addr.String(), Kind: networks.UseSeen})
	}
	return uses
}
