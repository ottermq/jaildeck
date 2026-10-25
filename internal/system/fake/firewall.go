package fake

import (
	"context"

	"github.com/ottermq/jaildeck/internal/firewall"
)

// Firewall is an in-memory PF with the test ruleset capture-pf-induced.sh
// loads on the VM: rules with counters, a NAT rule, a wildcard anchor with
// rules in it, an inline anchor, two tables and two pflog lines -- one from
// inside an anchor, which logs under its last name component only.
//
// Enabled, because a fake that showed "not loaded" -- the honest default for
// isengard -- would leave every other part of the page unbuildable.
type Firewall struct{}

func NewFirewall() *Firewall { return &Firewall{} }

func (f *Firewall) FirewallInfo(context.Context) (firewall.Info, error) {
	seconds := int64(4*86400 + 17*3600 + 2*60 + 3)
	return firewall.Info{
		State:      firewall.StateEnabled,
		ForSeconds: &seconds,
		StateTable: &firewall.StateTable{Current: 37, Searches: 918244, Inserts: 4120, Removals: 4083},
		Counters: map[string]int64{
			"match": 4562, "bad-offset": 0, "fragment": 0, "short": 0, "normalize": 0,
			"memory": 0, "state-mismatch": 3, "state-insert": 0, "state-limit": 0,
		},
	}, nil
}

func (f *Firewall) FirewallRules(_ context.Context, anchor string) (firewall.Ruleset, error) {
	switch anchor {
	case "":
		return firewall.Ruleset{Rules: []firewall.Rule{
			{Number: 0, Action: "pass", Label: "ssh",
				Text:        `pass in quick proto tcp from any to any port = ssh flags S/SA keep state label "ssh"`,
				Evaluations: 412, Packets: 18233, Bytes: 2_871_402, States: 2, StateCreations: 61,
				LastActive: "Sun Oct 25 18:04:44 2026"},
			{Number: 1, Action: "block", Label: "blocked-sources",
				Text:        `block drop in log quick from <jaildeck_test_blocked:2> to any label "blocked-sources"`,
				Evaluations: 412, Packets: 27, Bytes: 1620,
				LastActive: "Sun Oct 25 17:51:02 2026"},
			{Number: 2, Action: "pass", Label: "icmp-out-logged",
				Text:        `pass out log quick proto icmp all keep state label "icmp-out-logged"`,
				Evaluations: 3104, Packets: 96, Bytes: 8064, States: 1, StateCreations: 16,
				LastActive: "Sun Oct 25 18:04:42 2026"},
			{Number: 3, Action: "pass", Label: "outbound",
				Text:        `pass out all flags S/SA keep state label "outbound"`,
				Evaluations: 3088, Packets: 402_118, Bytes: 311_840_221, States: 31, StateCreations: 4021,
				LastActive: "Sun Oct 25 18:04:44 2026"},
			{Number: 4, Action: "pass",
				Text:        `pass in all flags S/SA keep state`,
				Evaluations: 385, Packets: 1204, Bytes: 98_120, States: 3, StateCreations: 22},
			{Number: 5, Action: "anchor", Text: `anchor "jaildeck/*" all`, Evaluations: 3473},
			{Number: 6, Action: "anchor", Text: `anchor "operator" all`, Evaluations: 3473},
		}}, nil
	case "jaildeck/web":
		return firewall.Ruleset{Rules: []firewall.Rule{
			{Number: 0, Action: "pass", Label: "web",
				Text:        `pass in proto tcp from any to any port = http flags S/SA keep state label "web"`,
				Evaluations: 385, Packets: 0},
			{Number: 1, Action: "pass", Label: "udp-anchored-logged",
				Text:        `pass out log quick proto udp from any to any port = discard keep state label "udp-anchored-logged"`,
				Evaluations: 3088, Packets: 2, Bytes: 102, States: 0, StateCreations: 1,
				LastActive: "Sun Oct 25 18:04:44 2026"},
		}}, nil
	case "operator":
		return firewall.Ruleset{Rules: []firewall.Rule{
			{Number: 0, Action: "pass", Label: "icmp-inline-anchor",
				Text: `pass in proto icmp all keep state label "icmp-inline-anchor"`},
		}}, nil
	default:
		return firewall.Ruleset{Rules: []firewall.Rule{}}, nil
	}
}

func (f *Firewall) FirewallNAT(_ context.Context, anchor string) (firewall.Ruleset, error) {
	if anchor != "" {
		return firewall.Ruleset{Rules: []firewall.Rule{}}, nil
	}
	return firewall.Ruleset{Rules: []firewall.Rule{
		{Number: 0, Action: "nat",
			Text:        `nat on vtnet0 inet from 10.99.0.0/24 to any -> (vtnet0) round-robin`,
			Evaluations: 3088},
	}}, nil
}

func (f *Firewall) FirewallAnchors(context.Context) ([]string, error) {
	return []string{"jaildeck", "jaildeck/web", "operator"}, nil
}

func (f *Firewall) FirewallTables(context.Context) ([]firewall.Table, error) {
	return []firewall.Table{
		{Name: "jaildeck_test_blocked", Flags: "-pa-r--", Persistent: true, Active: true, Referenced: true},
		{Name: "jaildeck_test_empty", Flags: "-pa----", Persistent: true, Active: true},
	}, nil
}

func (f *Firewall) FirewallTableAddresses(_ context.Context, name string) ([]string, error) {
	switch name {
	case "jaildeck_test_blocked":
		return []string{"192.0.2.10", "198.51.100.0/24"}, nil
	case "jaildeck_test_empty":
		return []string{}, nil
	}
	return nil, firewall.ErrNoSuchTable
}

func (f *Firewall) FirewallLogConfig(context.Context) (firewall.LogConfig, error) {
	return firewall.LogConfig{File: "/var/log/pflog", FlushDelaySeconds: 60}, nil
}

func (f *Firewall) FirewallLog(context.Context, string) (firewall.LogRead, error) {
	two, five := 2, 5
	return firewall.LogRead{Found: true, Lines: []firewall.LogLine{
		{Raw: "2026-10-25 18:04:42.882620 rule 2/0(match): pass out on vtnet0: 192.168.122.10 > 192.168.122.1: ICMP echo request, id 32266, seq 0, length 64",
			Time: "2026-10-25 18:04:42.882620", Rule: "2/0", RuleNumber: &two,
			Action: "pass", Direction: "out", Interface: "vtnet0",
			Packet: "192.168.122.10 > 192.168.122.1: ICMP echo request, id 32266, seq 0, length 64"},
		{Raw: "2026-10-25 18:04:44.974139 rule 5.web.1/0(match): pass out on vtnet0: 192.168.122.10.15917 > 192.168.122.1.9: UDP, length 9",
			Time: "2026-10-25 18:04:44.974139", Rule: "5.web.1/0", RuleNumber: &five, Anchor: "web",
			Action: "pass", Direction: "out", Interface: "vtnet0",
			Packet: "192.168.122.10.15917 > 192.168.122.1.9: UDP, length 9"},
	}}, nil
}

var _ firewall.FirewallSystem = (*Firewall)(nil)
