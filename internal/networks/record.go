package networks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/ottermq/jaildeck/internal/common"
)

// The record of tenant networks (JD-059): what it takes to put each one's
// bridge back after a reboot, which takes the bridges with it. While the host
// is up the bridges are what a network is read from (JD-011); the record is
// only read at boot, by `jaildeck networks restore`, and to say which recorded
// network has lost its bridge.
//
// It lives beside the anchor file, which Jail Deck writes as root and PF reads
// at boot, and not in the state directory, which holds the credential alone
// and which the front can pass through (JD-056).

// OperationWriteNetworkRecord is the audit log's name for a write of the
// record.
const OperationWriteNetworkRecord = "write-network-record"

// recordVersion is the file's format.
const recordVersion = 1

// Recorded is one tenant network as the record keeps it.
type Recorded struct {
	Name   string `json:"name"`
	Tenant string `json:"tenant,omitempty"`
	Bridge string `json:"bridge"`
	// Gateway is the host's address on the bridge with its prefix, as
	// `ifconfig <bridge> inet` takes it: 10.92.3.1/24.
	Gateway string `json:"gateway"`
}

type recordFile struct {
	Version  int        `json:"version"`
	Networks []Recorded `json:"networks"`
}

var tenantBridgeName = regexp.MustCompile(`^` + bridgePrefix + `[0-9]{1,4}$`)

func (s *Service) recordPath() string {
	return filepath.Join(s.settings.AnchorDir, "networks.json")
}

// readRecord is the record, and whether there is one at all: a host that
// never ran a Jail Deck with it has none.
func (s *Service) readRecord(ctx context.Context) ([]Recorded, bool, error) {
	content, err := s.system.NetworkRecord(ctx, s.recordPath())
	if err != nil {
		return nil, false, fmt.Errorf("reading %s: %w", s.recordPath(), err)
	}
	if strings.TrimSpace(content) == "" {
		return []Recorded{}, false, nil
	}
	var f recordFile
	if err := json.Unmarshal([]byte(content), &f); err != nil {
		return nil, true, fmt.Errorf("%s is not Jail Deck's network record: %w", s.recordPath(), err)
	}
	if f.Version != recordVersion {
		return nil, true, fmt.Errorf("%s is version %d of the network record; this Jail Deck reads version %d",
			s.recordPath(), f.Version, recordVersion)
	}
	for _, r := range f.Networks {
		if err := r.check(); err != nil {
			return nil, true, fmt.Errorf("%s: %w", s.recordPath(), err)
		}
	}
	if f.Networks == nil {
		f.Networks = []Recorded{}
	}
	return f.Networks, true, nil
}

// check refuses an entry restore could not act on safely: the bridge name
// and the gateway go on ifconfig's command line.
func (r Recorded) check() error {
	if !tenantBridgeName.MatchString(r.Bridge) {
		return fmt.Errorf("%q is not a tenant network's bridge", r.Bridge)
	}
	if !networkName.MatchString(r.Name) {
		return fmt.Errorf("%s: %q is not a network name", r.Bridge, r.Name)
	}
	if r.Tenant != "" && !tenantLabel.MatchString(r.Tenant) {
		return fmt.Errorf("%s: %q is not a tenant label", r.Bridge, r.Tenant)
	}
	if p, err := netip.ParsePrefix(r.Gateway); err != nil || !p.Addr().Is4() {
		return fmt.Errorf("%s: %q is not an IPv4 address with its prefix", r.Bridge, r.Gateway)
	}
	return nil
}

func (r Recorded) network() Network {
	n := Network{Name: r.Name, Tenant: r.Tenant, Bridge: r.Bridge, Members: make([]string, 0)}
	if p, err := netip.ParsePrefix(r.Gateway); err == nil {
		n.Subnet, n.Gateway = p.Masked().String(), p.Addr().String()
	}
	return n
}

// recordOf is a network read off the host, as the record keeps it; false for
// one whose bridge has no address, which there is nothing to put back for.
func recordOf(n Network) (Recorded, bool) {
	subnet, err1 := netip.ParsePrefix(n.Subnet)
	gateway, err2 := netip.ParseAddr(n.Gateway)
	if err1 != nil || err2 != nil {
		return Recorded{}, false
	}
	return Recorded{Name: n.Name, Tenant: n.Tenant, Bridge: n.Bridge,
		Gateway: netip.PrefixFrom(gateway, subnet.Bits()).String()}, true
}

// writeRecord replaces the record whole. Called with ensureMu held.
func (s *Service) writeRecord(ctx context.Context, list []Recorded) error {
	list = slices.Clone(list)
	slices.SortFunc(list, func(a, b Recorded) int { return strings.Compare(a.Bridge, b.Bridge) })
	content, err := json.MarshalIndent(recordFile{Version: recordVersion, Networks: list}, "", "  ")
	if err != nil {
		return err
	}
	command, err := s.system.WriteNetworkRecord(ctx, s.recordPath(), string(content)+"\n")
	s.log(ctx, OperationWriteNetworkRecord, s.recordPath(), command, err)
	return err
}

// syncRecord brings the record up to the host: every tenant network the host
// has now is in it as the host has it, and a recorded one whose bridge is
// missing stays, to be put back. A host without a record gets one this way.
// Called with ensureMu held.
func (s *Service) syncRecord(ctx context.Context, live []Network) ([]Recorded, error) {
	rec, found, err := s.readRecord(ctx)
	if err != nil {
		return nil, err
	}
	next := slices.Clone(rec)
	for _, n := range live {
		r, ok := recordOf(n)
		if !ok {
			continue
		}
		next = slices.DeleteFunc(next, func(o Recorded) bool { return o.Bridge == r.Bridge || o.Name == r.Name })
		next = append(next, r)
	}
	slices.SortFunc(next, func(a, b Recorded) int { return strings.Compare(a.Bridge, b.Bridge) })
	sorted := slices.Clone(rec)
	slices.SortFunc(sorted, func(a, b Recorded) int { return strings.Compare(a.Bridge, b.Bridge) })
	// Nothing to write: the record says what the host has, or there is no
	// record and no network to put in one.
	if slices.Equal(next, sorted) && (found || len(next) == 0) {
		return rec, nil
	}
	if err := s.writeRecord(ctx, next); err != nil {
		return nil, err
	}
	return next, nil
}

// missingFrom is every recorded network whose bridge the host does not have.
func missingFrom(rec []Recorded, bridges []TenantBridge) []Network {
	missing := make([]Network, 0)
	for _, r := range rec {
		if !slices.ContainsFunc(bridges, func(b TenantBridge) bool { return b.Name == r.Bridge }) {
			missing = append(missing, r.network())
		}
	}
	return missing
}

// CheckRecord is what Jail Deck runs as it starts: the record brought up to
// the host, and a warning for each recorded network whose bridge is missing,
// which `jaildeck networks restore` puts back. Nothing here stops the start.
func (s *Service) CheckRecord(ctx context.Context) {
	if s.system == nil {
		return
	}
	logger := common.Logger(ctx, "networks")
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	live, err := s.tenantNetworks(ctx)
	if err != nil {
		logger.Warn("could not read the tenant networks to record them", slog.Any("err", err))
		return
	}
	rec, err := s.syncRecord(ctx, live)
	if err != nil {
		logger.Warn("could not bring the network record up to date", slog.Any("err", err))
		return
	}
	bridges := make([]TenantBridge, 0, len(live))
	for _, n := range live {
		bridges = append(bridges, TenantBridge{Name: n.Bridge})
	}
	for _, n := range missingFrom(rec, bridges) {
		logger.Warn("a tenant network's bridge is missing; `jaildeck networks restore` puts it back",
			slog.String("network", n.Name), slog.String("bridge", n.Bridge))
	}
}

// Restored is what `jaildeck networks restore` did.
type Restored struct {
	// Created are the bridges it put back; Present the ones already there.
	Created []Recorded
	Present []Recorded
	// Problems are the networks it could not put back, and why.
	Problems []string
}

// Restore puts back the bridge of every recorded network that is missing,
// with its description and gateway: what the boot step runs, before jails
// and guests start (JD-059). A bridge that is there is left alone, so it is
// safe to run again. A missing bridge whose create fails is destroyed again,
// as a create from the API is, and the others are still tried.
func (s *Service) Restore(ctx context.Context) (Restored, error) {
	if s.system == nil {
		return Restored{}, s.unavailable()
	}
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	rec, _, err := s.readRecord(ctx)
	if err != nil {
		return Restored{}, err
	}
	bridges, err := s.system.TenantBridges(ctx)
	if err != nil {
		return Restored{}, readError("the tenant networks' bridges", err)
	}
	done := Restored{Created: []Recorded{}, Present: []Recorded{}, Problems: []string{}}
	for _, r := range rec {
		i := slices.IndexFunc(bridges, func(b TenantBridge) bool { return b.Name == r.Bridge })
		if i >= 0 {
			if have := bridges[i].Description; have != description(r.Name, r.Tenant) {
				done.Problems = append(done.Problems, fmt.Sprintf(
					"%s is there but its description is %q, not the network %s's; it was left as it is",
					r.Bridge, have, r.Name))
				continue
			}
			done.Present = append(done.Present, r)
			continue
		}
		command, err := s.system.CreateTenantBridge(ctx, r.Bridge, description(r.Name, r.Tenant))
		s.log(ctx, OperationCreateNetworkBridge, r.Bridge, command, err)
		if err == nil {
			command, err = s.system.SetBridgeAddress(ctx, r.Bridge, r.Gateway)
			s.log(ctx, OperationSetNetworkAddress, r.Bridge, command, err)
		}
		if err != nil {
			cmd, derr := s.system.DestroyBridge(ctx, r.Bridge)
			s.log(ctx, OperationDestroyNetworkBridge, r.Bridge, cmd, derr)
			done.Problems = append(done.Problems, fmt.Sprintf("%s (%s): %v", r.Bridge, r.Name, err))
			continue
		}
		done.Created = append(done.Created, r)
	}
	if len(done.Problems) > 0 {
		return done, fmt.Errorf("%d of %d networks could not be put back", len(done.Problems), len(rec))
	}
	return done, nil
}
