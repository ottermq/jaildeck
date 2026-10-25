package firewall

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/ottermq/jaildeck/internal/common"
)

// How many pflog lines a request may ask for, and how many it gets by default.
const (
	DefaultLogLines = 100
	MaxLogLines     = 1000
)

// Service answers what PF is doing. It changes nothing (JD-020, JD-034).
type Service struct {
	system FirewallSystem
}

func NewService(system FirewallSystem) *Service {
	return &Service{system: system}
}

func (s *Service) unavailable() error {
	return common.NewAppError(http.StatusNotImplemented,
		"this Jail Deck is running without the host access it needs to read the firewall")
}

// Report reads PF's state, its rules with their counters, NAT, every anchor's
// rules and the tables.
//
// When PF is not loaded that is the whole answer, and a 200: it is the common
// case, not a failure. An anchor that cannot be read is reported with its
// reason rather than failing the page, the way a jail that cannot be read is
// in the metrics report.
func (s *Service) Report(ctx context.Context) (Report, error) {
	if s.system == nil {
		return Report{}, s.unavailable()
	}
	info, err := s.system.FirewallInfo(ctx)
	if err != nil {
		return Report{}, readError("PF's status", err)
	}
	report := Report{
		State:   info.State,
		Rules:   make([]Rule, 0),
		NAT:     make([]Rule, 0),
		Anchors: make([]Anchor, 0),
		Tables:  make([]Table, 0),
	}
	if info.State == StateNotLoaded {
		return report, nil
	}
	report.ForSeconds = info.ForSeconds
	report.StateTable = info.StateTable
	report.Counters = info.Counters

	rules, err := s.system.FirewallRules(ctx, "")
	if err != nil {
		return Report{}, readError("the rules", err)
	}
	report.Rules = orEmpty(rules.Rules)
	report.Notices = append(report.Notices, rules.Notices...)

	nat, err := s.system.FirewallNAT(ctx, "")
	if err != nil {
		return Report{}, readError("the NAT rules", err)
	}
	report.NAT = orEmpty(nat.Rules)
	report.Notices = append(report.Notices, nat.Notices...)

	names, err := s.system.FirewallAnchors(ctx)
	if err != nil {
		return Report{}, readError("the anchors", err)
	}
	for _, name := range names {
		anchor, notices := s.anchor(ctx, name)
		report.Anchors = append(report.Anchors, anchor)
		report.Notices = append(report.Notices, notices...)
	}

	tables, err := s.system.FirewallTables(ctx)
	if err != nil {
		return Report{}, readError("the tables", err)
	}
	if tables != nil {
		report.Tables = tables
	}
	return report, nil
}

func (s *Service) anchor(ctx context.Context, name string) (Anchor, []string) {
	anchor := Anchor{Name: name, Rules: make([]Rule, 0), NAT: make([]Rule, 0)}
	rules, err := s.system.FirewallRules(ctx, name)
	if err != nil {
		anchor.Error = err.Error()
		return anchor, nil
	}
	nat, err := s.system.FirewallNAT(ctx, name)
	if err != nil {
		anchor.Error = err.Error()
		return anchor, nil
	}
	anchor.Rules, anchor.NAT = orEmpty(rules.Rules), orEmpty(nat.Rules)
	return anchor, append(rules.Notices, nat.Notices...)
}

// orEmpty keeps an empty ruleset an empty list in the JSON. nil marshals to
// null, and a client reads null as a broken field rather than "no rules".
func orEmpty(rules []Rule) []Rule {
	if rules == nil {
		return make([]Rule, 0)
	}
	return rules
}

// Table reads one table's addresses.
//
// The name must be one -s Tables listed. That is the real gate -- pfctl takes
// it as an argument, never through a shell -- and it also makes "no such
// table" a 404 decided by Jail Deck rather than a parse of pfctl's exit 255.
func (s *Service) Table(ctx context.Context, name string) (TableContents, error) {
	if s.system == nil {
		return TableContents{}, s.unavailable()
	}
	if err := validTableName(name); err != nil {
		return TableContents{}, err
	}
	tables, err := s.system.FirewallTables(ctx)
	if err != nil {
		return TableContents{}, readError("the tables", err)
	}
	listed := false
	for _, table := range tables {
		if table.Name == name {
			listed = true
			break
		}
	}
	if !listed {
		return TableContents{}, common.NewAppError(http.StatusNotFound,
			"PF has no table named "+strconv.Quote(name))
	}
	addresses, err := s.system.FirewallTableAddresses(ctx, name)
	if err != nil {
		return TableContents{}, readError("table "+strconv.Quote(name), err)
	}
	if addresses == nil {
		addresses = make([]string, 0)
	}
	return TableContents{Name: name, Addresses: addresses}, nil
}

// validTableName refuses what cannot be a PF table name. PF_TABLE_NAME_SIZE is
// 32 bytes including the terminator, so 31 is the longest.
func validTableName(name string) error {
	bad := name == "" || len(name) > 31 || strings.HasPrefix(name, "-") ||
		strings.ContainsFunc(name, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		})
	if bad {
		return common.NewAppError(http.StatusBadRequest,
			strconv.Quote(name)+" is not a PF table name")
	}
	return nil
}

// Log reads the most recent pflog lines.
//
// Never fresher than pflogd's last flush -- a minute by default -- and the
// response says so rather than Jail Deck signalling a daemon it does not own
// to flush sooner (JD-034). A log that could not be read to its end gives no
// lines at all: the lines that were read are the oldest, and showing them as
// recent would be worse than showing none.
func (s *Service) Log(ctx context.Context, lines int) (LogReport, error) {
	if s.system == nil {
		return LogReport{}, s.unavailable()
	}
	if lines == 0 {
		lines = DefaultLogLines
	}
	if lines < 1 || lines > MaxLogLines {
		return LogReport{}, common.NewAppError(http.StatusBadRequest,
			"lines must be between 1 and "+strconv.Itoa(MaxLogLines))
	}
	config, err := s.system.FirewallLogConfig(ctx)
	if err != nil {
		return LogReport{}, readError("pflog's configuration", err)
	}
	read, err := s.system.FirewallLog(ctx, config.File)
	if err != nil {
		return LogReport{}, readError("the pflog file", err)
	}
	report := LogReport{
		File:              config.File,
		Found:             read.Found,
		FlushDelaySeconds: config.FlushDelaySeconds,
		Truncated:         read.Truncated,
		Lines:             make([]LogLine, 0),
	}
	if !read.Found || read.Truncated {
		return report, nil
	}
	recent := read.Lines
	if len(recent) > lines {
		recent = recent[len(recent)-lines:]
	}
	report.Lines = append(report.Lines, recent...)
	return report, nil
}

func readError(what string, err error) error {
	if errors.Is(err, ErrNotLoaded) {
		return common.NewAppError(http.StatusConflict,
			"could not read "+what+": PF is not loaded")
	}
	return common.NewAppError(http.StatusInternalServerError,
		"could not read "+what+": "+err.Error())
}
