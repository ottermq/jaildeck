package freebsd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/system"
)

const (
	// defaultDmesgBoot is written once at boot by /etc/rc.d/dmesg. The live
	// kernel buffer wraps on a long-running host, and the VT-x line is among
	// the first things it loses.
	defaultDmesgBoot = "/var/run/dmesg.boot"
)

// defaultDevfsRulesFiles are where ruleset names live. The numbers come from
// the kernel; these files only label them, so a later file's name for a
// number replaces an earlier one's, as /etc/devfs.rules extends the defaults.
var defaultDevfsRulesFiles = []string{"/etc/defaults/devfs.rules", "/etc/devfs.rules"}

var devfsRulesetHeaderRE = regexp.MustCompile(`^\[([^=\]]+)=(\d+)\]`)

// runHost runs a host command. A non-zero exit becomes a CommandError
// carrying the command's own stderr, which is what an unknown check quotes.
func (a *Adapter) runHost(ctx context.Context, cmd system.Command) (system.CommandResult, error) {
	result, err := a.runner.Run(ctx, cmd)
	if err != nil {
		if msg := strings.TrimSpace(result.Stderr); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return result, &system.CommandError{Command: cmd.Name, Args: cmd.Args, Result: result, Err: err}
	}
	return result, nil
}

// exited reports whether the command ran and exited non-zero, as opposed to
// timing out or never starting.
func exited(result system.CommandResult, err error) bool {
	return err != nil && result.ExitCode > 0
}

func (a *Adapter) Sysctl(ctx context.Context, name string) (string, error) {
	result, err := a.runHost(ctx, system.Command{Name: "sysctl", Args: []string{"-n", name}})
	if err != nil {
		if exited(result, err) && strings.Contains(result.Stderr, "unknown oid") {
			return "", fmt.Errorf("%w: %w", host.ErrUnknownOID, err)
		}
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (a *Adapter) JailConfDefaults(ctx context.Context) (map[string]string, error) {
	_, defaults, err := listJailsFromConfFile(a.confFile)
	return defaults, err
}

// Bridges lists the interfaces in the bridge group. No bridges is exit 0 with
// nothing on stdout, which is an answer, not an error.
func (a *Adapter) Bridges(ctx context.Context) ([]string, error) {
	result, err := a.runHost(ctx, system.Command{Name: "ifconfig", Args: []string{"-g", "bridge"}})
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

// DevfsRulesets asks the kernel which rulesets are loaded. It is root-only:
// unprivileged, showsets exits 1 and that is an error, never "none".
func (a *Adapter) DevfsRulesets(ctx context.Context) ([]host.DevfsRuleset, error) {
	result, err := a.runHost(ctx, system.Command{Name: "devfs", Args: []string{"rule", "showsets"}})
	if err != nil {
		return nil, err
	}
	numbers, err := parseDevfsShowsets(result.Stdout)
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	for _, file := range a.devfsRulesFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			// /etc/devfs.rules does not exist on a stock host, and a ruleset
			// without a name is still a ruleset.
			continue
		}
		for number, name := range parseDevfsRulesetNames(string(content)) {
			names[number] = name
		}
	}
	rulesets := make([]host.DevfsRuleset, 0, len(numbers))
	for _, n := range numbers {
		rulesets = append(rulesets, host.DevfsRuleset{Name: names[n], Number: n})
	}
	return rulesets, nil
}

func parseDevfsShowsets(stdout string) ([]int, error) {
	lines := nonEmptyLines(stdout)
	numbers := make([]int, 0, len(lines))
	for _, line := range lines {
		n, err := strconv.Atoi(line)
		if err != nil {
			return nil, fmt.Errorf("parse devfs rule showsets: %q is not a ruleset number", line)
		}
		numbers = append(numbers, n)
	}
	return numbers, nil
}

// parseDevfsRulesetNames reads the [name=number] headers of a devfs.rules file.
func parseDevfsRulesetNames(content string) map[int]string {
	names := map[int]string{}
	for line := range strings.SplitSeq(content, "\n") {
		m := devfsRulesetHeaderRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		names[n] = strings.TrimSpace(m[1])
	}
	return names
}

// CPUVirtualization reads the VT-x line from the boot-time message log, and
// from the live buffer only when that file is absent. There is no pipeline to
// grep through, so the whole text is filtered here.
func (a *Adapter) CPUVirtualization(ctx context.Context) (host.CPUVirtualization, error) {
	content, err := os.ReadFile(a.dmesgBoot)
	if errors.Is(err, os.ErrNotExist) {
		result, runErr := a.runHost(ctx, system.Command{Name: "dmesg"})
		if runErr != nil {
			return host.CPUVirtualization{}, runErr
		}
		return parseVTx(result.Stdout), nil
	}
	if err != nil {
		return host.CPUVirtualization{}, err
	}
	return parseVTx(string(content)), nil
}

// parseVTx finds the kernel's "VT-x:" line. It is indented, so lines are
// trimmed before matching, and its features are compared as whole tokens:
// "UG" is a substring of every "root HUB" line, and "VMX" appears in the
// unrelated Features2 line. Only Intel's line is recognised; an AMD CPU
// prints "SVM:", which no captured host has shown, so it reads as unreported.
func parseVTx(text string) host.CPUVirtualization {
	var cpu host.CPUVirtualization
	for line := range strings.SplitSeq(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "VT-x:")
		if !ok {
			continue
		}
		// The live buffer can hold an earlier boot's line too; the last one
		// is the running kernel's.
		cpu = host.CPUVirtualization{Reported: true, Features: make([]string, 0)}
		rest = strings.TrimSpace(rest)
		if after, disabled := strings.CutPrefix(rest, "(disabled in BIOS)"); disabled {
			cpu.DisabledInBIOS = true
			rest = after
		}
		for token := range strings.SplitSeq(rest, ",") {
			if token = strings.TrimSpace(token); token != "" {
				cpu.Features = append(cpu.Features, token)
			}
		}
	}
	return cpu
}

// PFStatus answers both questions with one command. pfctl -s info exits 0
// whether PF is enabled or disabled, so the exit status says only whether
// /dev/pf exists; the Status field says the rest.
func (a *Adapter) PFStatus(ctx context.Context) (host.PFStatus, error) {
	result, err := a.runHost(ctx, system.Command{Name: "pfctl", Args: []string{"-s", "info"}})
	if err != nil {
		// Only a missing /dev/pf means "not loaded". Any other refusal, such
		// as a permission error, says nothing about PF itself.
		if exited(result, err) && strings.Contains(result.Stderr, "/dev/pf: No such file or directory") {
			return host.PFStatus{}, nil
		}
		return host.PFStatus{}, err
	}
	return parsePFInfo(result.Stdout)
}

func parsePFInfo(stdout string) (host.PFStatus, error) {
	first, _, _ := strings.Cut(stdout, "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(first), "Status:")
	if !ok {
		return host.PFStatus{}, fmt.Errorf("parse pfctl -s info: no Status field in %q", first)
	}
	fields := strings.Fields(rest)
	if len(fields) > 0 {
		switch fields[0] {
		case "Enabled":
			return host.PFStatus{Loaded: true, Enabled: true}, nil
		case "Disabled":
			return host.PFStatus{Loaded: true}, nil
		}
	}
	return host.PFStatus{}, fmt.Errorf("parse pfctl -s info: unrecognised status %q", strings.TrimSpace(rest))
}

// Pools lists pool names. On a host without ZFS zpool exists and exits 1, with
// a message that is not "command not found"; that error is passed on as is.
func (a *Adapter) Pools(ctx context.Context) ([]string, error) {
	result, err := a.runHost(ctx, system.Command{Name: "zpool", Args: []string{"list", "-H", "-o", "name"}})
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(result.Stdout), nil
}

// Datasets lists pool and everything beneath it in one call.
func (a *Adapter) Datasets(ctx context.Context, pool string) ([]host.Dataset, error) {
	result, err := a.runHost(ctx, system.Command{Name: "zfs", Args: []string{"list", "-H", "-o", "name,mountpoint", "-r", pool}})
	if err != nil {
		return nil, err
	}
	return parseZFSList(result.Stdout)
}

// parseZFSList reads -H output: one dataset per line, its fields separated by
// a single tab.
func parseZFSList(stdout string) ([]host.Dataset, error) {
	lines := nonEmptyLines(stdout)
	datasets := make([]host.Dataset, 0, len(lines))
	for _, line := range lines {
		name, mountpoint, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("parse zfs list: no mountpoint field in %q", line)
		}
		datasets = append(datasets, host.Dataset{Name: name, Mountpoint: mountpoint})
	}
	return datasets, nil
}

// CreateDataset runs zfs create. Properties become -o options in name order,
// so the same request always produces the same command.
func (a *Adapter) CreateDataset(ctx context.Context, name string, props map[string]string) (host.DatasetResult, error) {
	args := []string{"create"}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		args = append(args, "-o", k+"="+props[k])
	}
	cmd := system.Command{Name: "zfs", Args: append(args, name)}
	_, err := a.runHost(ctx, cmd)
	return host.DatasetResult{Dataset: name, Command: cmd.String()}, err
}

func nonEmptyLines(text string) []string {
	lines := make([]string, 0)
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
