package firewall

// PF's three states (JD-034). None of them is an error: not loaded is the
// common case -- neither isengard nor rohan has PF loaded -- and disabled with
// a ruleset is the case most worth showing.
const (
	StateNotLoaded = "not_loaded"
	StateDisabled  = "disabled"
	StateEnabled   = "enabled"
)

// Report is the whole of what GET /api/firewall answers.
type Report struct {
	State string `json:"state"`
	// ForSeconds is pfctl's own "Enabled for 4 days 17:02:03", in seconds:
	// the time since PF was last enabled or disabled -- or since boot, when
	// neither has happened. Measured: a PF loaded and never enabled reports
	// the host's uptime as "Disabled for", which is not how long it has been
	// disabled in any sense an operator means (JD-034). Absent when not loaded.
	ForSeconds *int64 `json:"for_seconds,omitempty"`

	StateTable *StateTable      `json:"state_table,omitempty"`
	Counters   map[string]int64 `json:"counters,omitempty"`

	// Rules is the main filter ruleset. NAT is separate because pfctl keeps
	// it separate: -s rules does not show a single NAT rule (JD-034).
	Rules   []Rule   `json:"rules"`
	NAT     []Rule   `json:"nat"`
	Anchors []Anchor `json:"anchors"`
	Tables  []Table  `json:"tables"`

	// Notices are anything pfctl printed to stderr while succeeding, other
	// than the ALTQ notice every -v call prints. Reported rather than
	// swallowed, and rather than failing the page over.
	Notices []string `json:"notices,omitempty"`
}

// StateTable is the "State Table" section of pfctl -s info.
type StateTable struct {
	Current  int64 `json:"current"`
	Searches int64 `json:"searches"`
	Inserts  int64 `json:"inserts"`
	Removals int64 `json:"removals"`
}

// Info is what the adapter reads from pfctl -s info.
type Info struct {
	State      string
	ForSeconds *int64
	StateTable *StateTable
	Counters   map[string]int64
}

// Rule is one rule as pfctl prints it, with its counters.
//
// Text is pfctl's own rendering and is not re-parsed: restating PF's grammar
// would be a second opinion that can disagree with the first (JD-034). Only
// what has to be a number is.
type Rule struct {
	// Number is the @N from -vv. pflog names a rule by it and nothing else
	// prints it, which is why the ruleset is read with -vv at all.
	Number int    `json:"number"`
	Text   string `json:"text"`
	// Action is the first word -- pass, block, match, anchor, nat, rdr -- for
	// the interface to colour by, and nothing cleverer.
	Action string `json:"action"`
	Label  string `json:"label,omitempty"`

	Evaluations    int64 `json:"evaluations"`
	Packets        int64 `json:"packets"`
	Bytes          int64 `json:"bytes"`
	States         int64 `json:"states"`
	StateCreations int64 `json:"state_creations"`
	// LastActive is pfctl's timestamp as printed, absent where it says N/A.
	LastActive string `json:"last_active,omitempty"`
}

// Ruleset is one read of rules, with whatever pfctl said on stderr while
// succeeding.
type Ruleset struct {
	Rules   []Rule
	Notices []string
}

// Anchor is one anchor and the rules loaded into it. The main ruleset shows
// `anchor "jaildeck/*" all` and stops there; what is inside needs reading
// separately (JD-034).
type Anchor struct {
	Name  string `json:"name"`
	Rules []Rule `json:"rules"`
	NAT   []Rule `json:"nat"`
	// Error says this one anchor could not be read -- it was listed and then
	// gone, most likely. The rest of the report still stands.
	Error string `json:"error,omitempty"`
}

// Table is one table's name and flags from pfctl -s Tables -v.
//
// No address count. -s Tables -vv reports one and it is wrong on 15.1 --
// Addresses: 1 for a table holding three -- so nothing is shown in its place
// and the count comes from the addresses themselves, when the table is opened
// (JD-034).
type Table struct {
	Name string `json:"name"`
	// Flags is the column as pfctl printed it, -pa-r-- and the like.
	Flags      string `json:"flags"`
	Persistent bool   `json:"persistent"`
	Active     bool   `json:"active"`
	Referenced bool   `json:"referenced"`
}

// TableContents is GET /api/firewall/tables/{name}.
type TableContents struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
}

// LogConfig is where pflog writes and how often it flushes, from rc.conf.
type LogConfig struct {
	File string
	// FlushDelaySeconds is pflogd's -d, 60 when pflog_flags does not set it.
	FlushDelaySeconds int
}

// LogRead is the adapter's read of the log file.
type LogRead struct {
	// Found is false when the file does not exist: nothing has ever been
	// logged, or pflogd is not running. Not an error.
	Found bool
	// Lines are every line in the file, oldest first, parsed as far as the
	// measured shape allows.
	Lines []LogLine
	// Truncated is true when the output did not fit in what Jail Deck will
	// read. Lines are then the oldest ones and must not be shown as recent.
	Truncated bool
}

// LogReport is GET /api/firewall/log.
type LogReport struct {
	File  string `json:"file"`
	Found bool   `json:"found"`
	// FlushDelaySeconds is how far behind the packets the file may be.
	// pflogd buffers, and Jail Deck does not signal it to flush (JD-034).
	FlushDelaySeconds int `json:"flush_delay_seconds"`
	// Truncated means the log could not be read to its end, and so no lines
	// are given: the ones that were read are the oldest, not the most recent.
	Truncated bool      `json:"truncated,omitempty"`
	Lines     []LogLine `json:"lines"`
}

// LogLine is one pflog line. Raw is always present; the parsed fields are
// absent when a line does not have the measured shape.
type LogLine struct {
	Raw  string `json:"raw"`
	Time string `json:"time,omitempty"`
	// Rule is as tcpdump printed it: 2/0, or 5.web.1/0 for a rule inside an
	// anchor. RuleNumber is the main-ruleset number before the first dot or
	// slash, which is unambiguous; Anchor is only the anchor's last name
	// component, which is not (JD-034).
	Rule       string `json:"rule,omitempty"`
	RuleNumber *int   `json:"rule_number,omitempty"`
	Anchor     string `json:"anchor,omitempty"`
	Action     string `json:"action,omitempty"`
	Direction  string `json:"direction,omitempty"`
	Interface  string `json:"interface,omitempty"`
	Packet     string `json:"packet,omitempty"`
}
