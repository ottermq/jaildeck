package vulns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The shapes a package's name and version may have before they reach pkg
// audit. Both come from a database a jail's root controls, so they are
// checked here rather than trusted: neither may start with "-", which pkg
// would take for an option, and neither holds a space, a quote or anything
// else a name and a version never need. They go to pkg as separate
// arguments, never through a shell.
var (
	nameShape    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	versionShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._,+]{0,63}$`)
)

// MaxPackages is more packages than any tree has -- a full desktop is about
// two thousand -- and few enough that a database claiming more is refused
// rather than turned into an argument list.
const MaxPackages = 10000

// ErrTooManyPackages is a package list longer than MaxPackages.
var ErrTooManyPackages = errors.New("more packages than Jail Deck audits")

// ParseInstalled reads `pkg query '%n\t%v'`: one package per line, its name
// and version separated by a tab. A line that is not shaped like a package
// is skipped and counted, never passed on.
func ParseInstalled(out []byte) (pkgs []Package, skipped int, err error) {
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		name, version, ok := strings.Cut(line, "\t")
		if !ok || !nameShape.MatchString(name) || !versionShape.MatchString(version) {
			skipped++
			continue
		}
		if len(pkgs) == MaxPackages {
			return nil, skipped, ErrTooManyPackages
		}
		pkgs = append(pkgs, Package{Name: name, Version: version})
	}
	return pkgs, skipped, nil
}

// auditJSON is `pkg audit --raw=json-compact`, as pkg 2.x writes it.
type auditJSON struct {
	Count    *int `json:"pkg_count"`
	Packages map[string]struct {
		Version string `json:"version"`
		Issues  []struct {
			Affected    []string `json:"Affected versions"`
			Description string   `json:"description"`
			CVE         []string `json:"cve"`
			URL         string   `json:"url"`
		} `json:"issues"`
		Reverse []string `json:"reverse dependencies"`
	} `json:"packages"`
}

// ParseAudit reads `pkg audit --raw=json-compact`. Output without a
// pkg_count is not an audit -- pkg's error for a missing database is plain
// text -- and is refused, so a failure is never read as nothing found.
func ParseAudit(out []byte) ([]Finding, error) {
	var raw auditJSON
	dec := json.NewDecoder(bytes.NewReader(out))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading pkg audit's output: %w", err)
	}
	if raw.Count == nil {
		return nil, errors.New("pkg audit's output has no pkg_count")
	}
	findings := make([]Finding, 0, len(raw.Packages))
	for name, p := range raw.Packages {
		f := Finding{Name: name, Version: p.Version, Issues: make([]Issue, 0, len(p.Issues)),
			ReverseDependencies: nonNil(p.Reverse)}
		for _, i := range p.Issues {
			f.Issues = append(f.Issues, Issue{Description: i.Description, CVEs: nonNil(i.CVE),
				URL: i.URL, Affected: nonNil(i.Affected)})
		}
		findings = append(findings, f)
	}
	if len(findings) != *raw.Count {
		return nil, fmt.Errorf("pkg audit counted %d vulnerable packages and listed %d", *raw.Count, len(findings))
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Name < findings[j].Name })
	return findings, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Valid is whether p is shaped like a package, as ParseInstalled checks.
// The adapter checks it again before p becomes an argument.
func (p Package) Valid() bool {
	return nameShape.MatchString(p.Name) && versionShape.MatchString(p.Version)
}
