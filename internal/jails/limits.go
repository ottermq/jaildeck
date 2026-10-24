package jails

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Limits are a jail's resource limits: rctl rules plus a cpuset (JD-018).
//
// They are applied by the generated stanza, not by this process --
// exec.poststart adds them, exec.prestop takes the rules away again. Measured
// on 15.1-RELEASE-p5: a jail started with `service jail start` from a shell
// then gets the same limits, and a jail whose rules cannot be applied does
// not start at all, because jail(8) destroys a jail whose poststart fails.
// An adapter applying them afterwards could only have managed the second by
// stopping a jail it had just started.
type Limits struct {
	// MemoryUse is resident memory and VMemoryUse is address space. Both are
	// a byte count with an optional K, M, G or T suffix, in either case --
	// what rctl(8) accepts, measured.
	MemoryUse  string `json:"memoryuse,omitempty"`
	VMemoryUse string `json:"vmemoryuse,omitempty"`
	MaxProc    int    `json:"maxproc,omitempty"`
	OpenFiles  int    `json:"openfiles,omitempty"`
	// PCPU is percent of one core: 100 is a whole core, and a sixteen-core
	// host takes up to 1600. How many the host has is the host's to judge,
	// and it refuses more than it has (JD-012).
	PCPU int `json:"pcpu,omitempty"`
	// CPUSet is the cpu list the jail is pinned to: "0-3", "0-3,8", "2". It
	// is not an rctl rule and needs no accounting, so it is the one limit a
	// host with kern.racct.enable=0 can still have.
	CPUSet string `json:"cpuset,omitempty"`
}

// rctl reports whether anything here becomes an rctl rule, which is what
// decides whether the host needs kern.racct.enable at all.
func (l Limits) rctl() bool {
	return l.MemoryUse != "" || l.VMemoryUse != "" ||
		l.MaxProc > 0 || l.OpenFiles > 0 || l.PCPU > 0
}

func (l Limits) empty() bool {
	return !l.rctl() && l.CPUSet == ""
}

// Rules are the rctl rules for a jail of this name, in a fixed order so that
// the same spec generates the same stanza every time.
//
// Every rule is a deny. pcpu looks like it should throttle -- rctl(8)
// describes throttle as the action for %cpu -- but `pcpu:throttle` on a jail
// is "Operation not supported" and `pcpu:deny` is accepted. Measured.
func (l Limits) Rules(name string) []string {
	rules := make([]string, 0, 5)
	add := func(resource, amount string) {
		if amount != "" {
			rules = append(rules, fmt.Sprintf("jail:%s:%s:deny=%s", name, resource, amount))
		}
	}
	add("memoryuse", l.MemoryUse)
	add("vmemoryuse", l.VMemoryUse)
	add("maxproc", positive(l.MaxProc))
	add("openfiles", positive(l.OpenFiles))
	add("pcpu", positive(l.PCPU))
	return rules
}

func positive(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// amountRE is what rctl(8) takes as a quantity: a byte count, optionally
// suffixed. Checked here rather than left to the host because rctl's own
// refusal arrives after the dataset has been cloned, and the whole order of
// a create is that what can be refused is refused first (JD-030).
var amountRE = regexp.MustCompile(`^[0-9]+[KMGTkmgt]?$`)

// cpuRangeRE is one item of a cpu list: a number, or a range of them.
var cpuRangeRE = regexp.MustCompile(`^([0-9]+)(?:-([0-9]+))?$`)

// normalize validates the limits and returns them as they will be applied.
//
// The two string fields are validated here rather than by the host, which is
// the exception to JD-012 and earns it by the error messages: rctl answers a
// malformed size with "invalid numeric value", which is clear enough, but
// cpuset answers a malformed cpu list with "Resource deadlock avoided", which
// tells an operator nothing at all. Measured, both.
func (l Limits) normalize() (Limits, error) {
	l.MemoryUse = strings.TrimSpace(l.MemoryUse)
	l.VMemoryUse = strings.TrimSpace(l.VMemoryUse)
	l.CPUSet = strings.TrimSpace(l.CPUSet)

	for _, size := range []struct {
		field string
		value string
	}{
		{"memoryuse", l.MemoryUse},
		{"vmemoryuse", l.VMemoryUse},
	} {
		if size.value != "" && !amountRE.MatchString(size.value) {
			return Limits{}, badSpec(fmt.Sprintf(
				"limits.%s %q is not a size; it has to be a number of bytes, optionally with a K, M, G or T suffix",
				size.field, size.value))
		}
	}

	for _, count := range []struct {
		field string
		value int
	}{
		{"maxproc", l.MaxProc},
		{"openfiles", l.OpenFiles},
		{"pcpu", l.PCPU},
	} {
		if count.value < 0 {
			return Limits{}, badSpec(fmt.Sprintf(
				"limits.%s cannot be %d; leave it out to ask for no limit", count.field, count.value))
		}
	}

	if l.CPUSet != "" {
		if err := checkCPUList(l.CPUSet); err != nil {
			return Limits{}, err
		}
	}
	return l, nil
}

// checkCPUList reads a cpuset(1) cpu list: comma-separated numbers and
// ranges. Which cpus the host actually has is the host's answer, and it
// gives it -- "setaffinity: Invalid argument" for a cpu out of range.
func checkCPUList(list string) error {
	malformed := func(why string) error {
		return badSpec(fmt.Sprintf(
			"limits.cpuset %q is not a cpu list: %s. It has to be numbers and ranges, as in \"0-3\", \"2\" or \"0-3,8\"",
			list, why))
	}
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		matches := cpuRangeRE.FindStringSubmatch(item)
		if matches == nil {
			return malformed(fmt.Sprintf("%q is neither a cpu nor a range of them", item))
		}
		if matches[2] == "" {
			continue
		}
		low, _ := strconv.Atoi(matches[1])
		high, _ := strconv.Atoi(matches[2])
		if low > high {
			return malformed(fmt.Sprintf("the range %q runs backwards", item))
		}
	}
	return nil
}

// hooks are the commands that apply a jail's limits and take them away
// again: exec.poststart adds every rctl rule and pins the cpuset,
// exec.prestop removes the rules.
//
// There is no prestop for a cpuset. It belongs to the jail and goes when the
// jail does, whereas an rctl rule is keyed to the name and outlives it --
// measured: a jail removed without `rctl -r` leaves its rules in the kernel,
// and the next jail of that name inherits them silently.
func (l Limits) hooks(name string) (poststart, prestop []string) {
	rules := l.Rules(name)
	poststart = make([]string, 0, len(rules)+1)
	for _, rule := range rules {
		poststart = append(poststart, "rctl -a "+rule)
	}
	if l.CPUSet != "" {
		poststart = append(poststart, "cpuset -l "+l.CPUSet+" -j "+name)
	}
	if len(rules) > 0 {
		prestop = []string{RemoveRulesCommand(name)}
	}
	return poststart, prestop
}

// RemoveRulesCommand is the one command that clears a jail's rctl rules. It
// is exported because it is also the remedy Jail Deck hands an operator when
// it refuses a name whose rules are still in the kernel: the message and the
// stanza have to say the same thing.
func RemoveRulesCommand(name string) string {
	return "rctl -r jail:" + name
}
