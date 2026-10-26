package freebsd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/firewall"
	"github.com/ottermq/jaildeck/internal/system"
)

// pfctlTimeout bounds one read. Each is an ioctl and some formatting; a
// ruleset of thousands of rules is still well under a second.
const pfctlTimeout = 15 * time.Second

// pflogTimeout bounds reading the whole log with tcpdump, which is a file
// read of at most about a megabyte between rotations.
const pflogTimeout = 30 * time.Second

// pflogMaxOutput is how much tcpdump text Jail Deck will read. /var/log/pflog
// is rotated at 1000 KB by /etc/newsyslog.conf.d/pf.conf on every host
// measured, and tcpdump's text is about the size of the pcap it decodes; the
// runner's own default of 1 MiB would cut it off, and it keeps the *start*,
// which is the oldest lines. Sixteen times the rotation size leaves room for
// an hour's growth before newsyslog runs, and anything past it is reported as
// unreadable rather than shown as recent (JD-034).
const pflogMaxOutput = 16 << 20

// The two lines every -v prints to stderr on a kernel without ALTQ, with
// exit 0. Expected, and not a notice worth reporting (JD-034).
var altqNotices = map[string]bool{
	"No ALTQ support in kernel":       true,
	"ALTQ related functions disabled": true,
}

// pfctlRead runs one read-only pfctl command and sorts out the ways it answers
// besides plain output: PF not loaded, an anchor or table that does not
// exist, and whatever else it said on stderr while succeeding.
//
// Every caller passes a fixed show-style argument list; nothing here takes a
// flag from a caller. The firewall tests check every command this file can
// run for a write flag (JD-034).
func (a *Adapter) pfctlRead(ctx context.Context, args ...string) (string, []string, error) {
	cmd := system.Command{Name: "pfctl", Args: args, Timeout: pfctlTimeout}
	result, err := a.runner.Run(ctx, cmd)

	notices := make([]string, 0)
	for _, line := range nonEmptyLines(result.Stderr) {
		line = strings.TrimSpace(line)
		switch {
		case strings.Contains(line, "/dev/pf: No such file or directory"):
			return "", nil, firewall.ErrNotLoaded
		// Exit 0 for this one, measured: the message is the only sign.
		case strings.HasSuffix(line, "Anchor does not exist."):
			return "", nil, firewall.ErrNoSuchAnchor
		case strings.HasSuffix(line, "Table does not exist."):
			return "", nil, firewall.ErrNoSuchTable
		case altqNotices[line]:
		default:
			notices = append(notices, line)
		}
	}
	if err != nil {
		if len(notices) > 0 {
			err = fmt.Errorf("%w: %s", err, strings.Join(notices, "; "))
		}
		return "", nil, &system.CommandError{Command: cmd.Name, Args: cmd.Args, Result: result, Err: err}
	}
	return result.Stdout, notices, nil
}

var (
	pfStatusRE    = regexp.MustCompile(`^Status: (Enabled|Disabled)(?: for (\d+) days (\d+):(\d+):(\d+))?`)
	pfInfoLineRE  = regexp.MustCompile(`^\s+(\S.*?)\s{2,}(\d+)(?:\s|$)`)
	pfColumnGapRE = regexp.MustCompile(`\s{2,}`)
)

// FirewallInfo reads pfctl -s info: whether PF is loaded and enabled, for how
// long, and its counters. Not -v, which adds nothing the view shows and the
// ALTQ notice to stderr.
func (a *Adapter) FirewallInfo(ctx context.Context) (firewall.Info, error) {
	out, _, err := a.pfctlRead(ctx, "-s", "info")
	if errors.Is(err, firewall.ErrNotLoaded) {
		// The common case on the hosts measured, and an answer.
		return firewall.Info{State: firewall.StateNotLoaded}, nil
	}
	if err != nil {
		return firewall.Info{}, err
	}

	lines := strings.Split(out, "\n")
	status := pfStatusRE.FindStringSubmatch(lines[0])
	if status == nil {
		return firewall.Info{}, fmt.Errorf("parse pfctl -s info: no Status in %q", lines[0])
	}
	info := firewall.Info{
		State:      firewall.StateDisabled,
		StateTable: &firewall.StateTable{},
		Counters:   map[string]int64{},
	}
	if status[1] == "Enabled" {
		info.State = firewall.StateEnabled
	}
	if status[2] != "" {
		var parts [4]int64
		for i := range parts {
			parts[i], _ = strconv.ParseInt(status[i+2], 10, 64)
		}
		seconds := parts[0]*86400 + parts[1]*3600 + parts[2]*60 + parts[3]
		info.ForSeconds = &seconds
	}

	section := ""
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			// A section heading sits at the margin; its rows are indented.
			// Some headings carry column titles on the same line --
			// "State Table   Total   Rate" -- so the name is what comes
			// before the first run of spaces.
			section = strings.TrimSpace(pfColumnGapRE.Split(line, 2)[0])
			continue
		}
		row := pfInfoLineRE.FindStringSubmatch(line)
		if row == nil {
			// Syncookies and the like carry words, not totals.
			continue
		}
		total, _ := strconv.ParseInt(row[2], 10, 64)
		switch section {
		case "State Table":
			switch row[1] {
			case "current entries":
				info.StateTable.Current = total
			case "searches":
				info.StateTable.Searches = total
			case "inserts":
				info.StateTable.Inserts = total
			case "removals":
				info.StateTable.Removals = total
			}
		case "Counters":
			info.Counters[row[1]] = total
		}
	}
	return info, nil
}

// FirewallRules reads the filter rules, in one anchor or the main ruleset.
func (a *Adapter) FirewallRules(ctx context.Context, anchor string) (firewall.Ruleset, error) {
	return a.ruleset(ctx, anchor, "rules")
}

// FirewallNAT reads the NAT rules. Separately, because -s rules does not show
// a single one of them (JD-034).
func (a *Adapter) FirewallNAT(ctx context.Context, anchor string) (firewall.Ruleset, error) {
	return a.ruleset(ctx, anchor, "nat")
}

func (a *Adapter) ruleset(ctx context.Context, anchor, kind string) (firewall.Ruleset, error) {
	args := []string{"-s", kind, "-vv"}
	if anchor != "" {
		args = append([]string{"-a", anchor}, args...)
	}
	out, notices, err := a.pfctlRead(ctx, args...)
	if err != nil {
		return firewall.Ruleset{}, err
	}
	rules, err := parseRules(out)
	if err != nil {
		return firewall.Ruleset{}, fmt.Errorf("parse pfctl %s: %w", strings.Join(args, " "), err)
	}
	return firewall.Ruleset{Rules: rules, Notices: notices}, nil
}

var (
	ruleHeadRE    = regexp.MustCompile(`^@(\d+) (.+)$`)
	ruleCounterRE = regexp.MustCompile(`(State Creations|Evaluations|Packets|Bytes|States):\s+(\d+)`)
	ruleActiveRE  = regexp.MustCompile(`^\s*\[ Last Active Time: (.*?) \]\s*$`)
	ruleLabelRE   = regexp.MustCompile(`\blabel "((?:[^"\\]|\\.)*)"`)
)

// parseRules reads pfctl -vv's rule listing: a line per rule starting with its
// number, then bracketed counter lines under it.
//
// The rule's own text is kept as pfctl printed it. Only the counters, the
// number, the label and the first word are taken out of it.
func parseRules(out string) ([]firewall.Rule, error) {
	rules := make([]firewall.Rule, 0)
	for _, line := range nonEmptyLines(out) {
		if head := ruleHeadRE.FindStringSubmatch(line); head != nil {
			number, _ := strconv.Atoi(head[1])
			rule := firewall.Rule{Number: number, Text: head[2], Action: strings.Fields(head[2])[0]}
			if label := ruleLabelRE.FindStringSubmatch(head[2]); label != nil {
				rule.Label = label[1]
			}
			rules = append(rules, rule)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "[") || len(rules) == 0 {
			// pfctl prints a rule on one line, always. Anything else means
			// this is not the shape that was measured, and a view built on a
			// guess about it would be wrong in ways nobody would notice.
			return nil, fmt.Errorf("unrecognised line %q", line)
		}
		rule := &rules[len(rules)-1]
		if active := ruleActiveRE.FindStringSubmatch(line); active != nil {
			if active[1] != "N/A" {
				rule.LastActive = active[1]
			}
			continue
		}
		for _, counter := range ruleCounterRE.FindAllStringSubmatch(line, -1) {
			value, _ := strconv.ParseInt(counter[2], 10, 64)
			switch counter[1] {
			case "Evaluations":
				rule.Evaluations = value
			case "Packets":
				rule.Packets = value
			case "Bytes":
				rule.Bytes = value
			case "States":
				rule.States = value
			case "State Creations":
				rule.StateCreations = value
			}
		}
	}
	return rules, nil
}

// FirewallAnchors reads pfctl -s Anchors: every anchor, flat, with wildcard
// parents and inline anchors included.
func (a *Adapter) FirewallAnchors(ctx context.Context) ([]string, error) {
	out, _, err := a.pfctlRead(ctx, "-s", "Anchors")
	if err != nil {
		return nil, err
	}
	anchors := make([]string, 0)
	for _, line := range nonEmptyLines(out) {
		anchors = append(anchors, strings.TrimSpace(line))
	}
	return anchors, nil
}

// FirewallTables reads pfctl -s Tables -v: a flags column and a name.
//
// Not -vv. Its statistics are wrong on 15.1 -- Addresses: 1 for a table
// holding three, References: Rules: 0 for a table a rule uses -- and its flags
// are the same as -v's (JD-034).
func (a *Adapter) FirewallTables(ctx context.Context) ([]firewall.Table, error) {
	out, _, err := a.pfctlRead(ctx, "-s", "Tables", "-v")
	if err != nil {
		return nil, err
	}
	tables := make([]firewall.Table, 0)
	for _, line := range nonEmptyLines(out) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("parse pfctl -s Tables -v: unrecognised line %q", line)
		}
		flags := fields[0]
		tables = append(tables, firewall.Table{
			Name:  fields[1],
			Flags: flags,
			// By letter, not by position: the letters are distinct, and a
			// position is a printf that a release could reorder.
			Persistent: strings.Contains(flags, "p"),
			Active:     strings.Contains(flags, "a"),
			Referenced: strings.Contains(flags, "r"),
		})
	}
	return tables, nil
}

// FirewallTableAddresses reads pfctl -t <name> -T show: one address or
// network per line, indented.
func (a *Adapter) FirewallTableAddresses(ctx context.Context, name string) ([]string, error) {
	out, _, err := a.pfctlRead(ctx, "-t", name, "-T", "show")
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0)
	for _, line := range nonEmptyLines(out) {
		addresses = append(addresses, strings.TrimSpace(line))
	}
	return addresses, nil
}

// pflogDefaultFlushDelay is pflogd's -d when pflog_flags does not set it
// (pflogd(8)).
const pflogDefaultFlushDelay = 60

// FirewallLogConfig reads where pflog writes and how often pflogd flushes.
//
//	sysrc -n pflog_logfile pflog_flags
//
// One line each, measured, with an empty line for flags that are not set.
// pflog_logfile has a default in /etc/defaults/rc.conf, so it is always there.
func (a *Adapter) FirewallLogConfig(ctx context.Context) (firewall.LogConfig, error) {
	result, err := a.runHost(ctx, system.Command{
		Name:    "sysrc",
		Args:    []string{"-n", "pflog_logfile", "pflog_flags"},
		Timeout: sysrcTimeout,
	})
	if err != nil {
		return firewall.LogConfig{}, err
	}
	lines := strings.Split(result.Stdout, "\n")
	config := firewall.LogConfig{
		File:              strings.TrimSpace(lines[0]),
		FlushDelaySeconds: pflogDefaultFlushDelay,
	}
	if config.File == "" {
		return firewall.LogConfig{}, errors.New("sysrc reported no pflog_logfile")
	}
	if len(lines) > 1 {
		if delay, ok := flushDelay(lines[1]); ok {
			config.FlushDelaySeconds = delay
		}
	}
	return config, nil
}

// flushDelay finds pflogd's -d in its flags, as "-d 30" or "-d30".
func flushDelay(flags string) (int, bool) {
	fields := strings.Fields(flags)
	for i, field := range fields {
		value := ""
		switch {
		case field == "-d" && i+1 < len(fields):
			value = fields[i+1]
		case strings.HasPrefix(field, "-d") && len(field) > 2:
			value = field[2:]
		default:
			continue
		}
		if delay, err := strconv.Atoi(value); err == nil && delay > 0 {
			return delay, true
		}
	}
	return 0, false
}

// FirewallLog reads the whole pflog file with tcpdump.
//
// -tttt, not -ttt: -ttt is the time since the previous packet, so every read
// would start at 00:00:00 (JD-034). There is no way to ask tcpdump for the
// last lines of a file, so it is read whole and the caller keeps the tail.
func (a *Adapter) FirewallLog(ctx context.Context, file string) (firewall.LogRead, error) {
	cmd := system.Command{
		Name:      "tcpdump",
		Args:      []string{"-n", "-e", "-tttt", "-r", file},
		Timeout:   pflogTimeout,
		MaxOutput: pflogMaxOutput,
	}
	result, err := a.runner.Run(ctx, cmd)
	if err != nil {
		if exited(result, err) && strings.Contains(result.Stderr, "No such file or directory") {
			// Nothing has ever been logged here, or pflogd is not running.
			return firewall.LogRead{Found: false, Lines: make([]firewall.LogLine, 0)}, nil
		}
		if msg := strings.TrimSpace(result.Stderr); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return firewall.LogRead{}, &system.CommandError{Command: cmd.Name, Args: cmd.Args, Result: result, Err: err}
	}
	if result.StdoutTruncated {
		// What was read is the beginning of the file. Say so and give none.
		return firewall.LogRead{Found: true, Truncated: true, Lines: make([]firewall.LogLine, 0)}, nil
	}
	lines := make([]firewall.LogLine, 0)
	for _, line := range nonEmptyLines(result.Stdout) {
		lines = append(lines, parseLogLine(line))
	}
	return firewall.LogRead{Found: true, Lines: lines}, nil
}

// "2026-10-25 18:04:44.974139 rule 5.web.1/0(match): pass out on vtnet0: <packet>"
var pflogLineRE = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d+) rule (\S+?)\([^)]*\): (\S+) (in|out) on (\S+): (.*)$`)

// parseLogLine takes apart one tcpdump line from a pflog file, as far as the
// measured shape goes. A line that does not have it is kept whole in Raw and
// nothing else, rather than dropped or guessed at.
func parseLogLine(raw string) firewall.LogLine {
	line := firewall.LogLine{Raw: raw}
	match := pflogLineRE.FindStringSubmatch(raw)
	if match == nil {
		return line
	}
	line.Time, line.Rule = match[1], match[2]
	line.Action, line.Direction, line.Interface, line.Packet = match[3], match[4], match[5], match[6]

	// "2/0" is main rule 2. "5.web.1/0" is main rule 5 -- the anchor
	// rule -- then the anchor's last name component, then the rule inside
	// it. Only the first is unambiguous: two anchors named a/web and b/web
	// both log as "web".
	numbered := strings.SplitN(line.Rule, "/", 2)[0]
	parts := strings.Split(numbered, ".")
	if number, err := strconv.Atoi(parts[0]); err == nil {
		line.RuleNumber = &number
	}
	if len(parts) >= 3 {
		line.Anchor = strings.Join(parts[1:len(parts)-1], ".")
	}
	return line
}

var _ firewall.FirewallSystem = (*Adapter)(nil)
