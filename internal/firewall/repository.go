package firewall

import (
	"context"
	"errors"
)

// FirewallSystem reads PF. Nothing more: there is no method here that could
// change the firewall, and the adapter is tested for never running a command
// that would (JD-020, JD-034). A write path is phase 2, and it needs a new
// method and a reason -- not a flag added to one of these.
//
// Named Firewall* because the FreeBSD adapter already has a PFStatus for the
// host domain's readiness check, which asks a different question of the same
// command.
type FirewallSystem interface {
	// FirewallInfo is pfctl -s info. PF not being loaded is an Info whose
	// State says so, not an error.
	FirewallInfo(ctx context.Context) (Info, error)
	// FirewallRules is pfctl -s rules -vv, in the anchor named, or the main
	// ruleset when the name is empty.
	FirewallRules(ctx context.Context, anchor string) (Ruleset, error)
	// FirewallNAT is pfctl -s nat -vv, likewise.
	FirewallNAT(ctx context.Context, anchor string) (Ruleset, error)
	// FirewallAnchors is pfctl -s Anchors: every anchor, flat.
	FirewallAnchors(ctx context.Context) ([]string, error)
	// FirewallTables is pfctl -s Tables -v.
	FirewallTables(ctx context.Context) ([]Table, error)
	// FirewallTableAddresses is pfctl -t <name> -T show.
	FirewallTableAddresses(ctx context.Context, name string) ([]string, error)
	// FirewallLogConfig reads pflog_logfile and pflog_flags from rc.conf.
	FirewallLogConfig(ctx context.Context) (LogConfig, error)
	// FirewallLog reads the log file with tcpdump.
	FirewallLog(ctx context.Context, file string) (LogRead, error)
}

var (
	// ErrNoSuchAnchor is pfctl's "Anchor does not exist." -- which it prints
	// with exit status 0, so it is recognised by the message (JD-034).
	ErrNoSuchAnchor = errors.New("anchor does not exist")
	// ErrNoSuchTable is pfctl's "Table does not exist.", exit 255.
	ErrNoSuchTable = errors.New("table does not exist")
	// ErrNotLoaded is a read that needs PF when PF is not loaded.
	ErrNotLoaded = errors.New("PF is not loaded")
)
