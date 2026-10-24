package freebsd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// ifconfigTimeout bounds an interface command. Each one is a single ioctl
// against the kernel (JD-014).
const ifconfigTimeout = 15 * time.Second

// maxEpairUnit is where the search for a free unit gives up. A host with
// 1024 jails on epairs has a problem this error will not make worse, and an
// unbounded loop on a misreporting ifconfig would be the worse failure.
const maxEpairUnit = 1024

// epairUnitRE finds the unit in an interface name or a configuration line:
// epair7a, epair7b and `ifconfig epair7 create` all yield 7.
var epairUnitRE = regexp.MustCompile(`\bepair(\d+)`)

// FreeEpairUnit returns an epair unit nothing is using and no jail
// configuration already claims.
//
// Both halves are needed. The host's interfaces answer for jails that are
// running; the configuration files answer for jails that are not, whose
// stanzas name a unit that will be created the moment they start. Handing
// out a unit a stopped jail owns would work until that jail was started.
func (a *Adapter) FreeEpairUnit(ctx context.Context) (string, error) {
	taken := map[int]bool{}

	result, err := a.runHost(ctx, system.Command{
		Name:    "ifconfig",
		Args:    []string{"-g", "epair"},
		Timeout: ifconfigTimeout,
	})
	if err != nil {
		return "", common.NewAppError(http.StatusInternalServerError,
			"could not list the host's epairs: "+err.Error())
	}
	for _, match := range epairUnitRE.FindAllStringSubmatch(result.Stdout, -1) {
		if unit, err := strconv.Atoi(match[1]); err == nil {
			taken[unit] = true
		}
	}

	claimed, err := a.unitsInConfigs()
	if err != nil {
		return "", err
	}
	for unit := range claimed {
		taken[unit] = true
	}

	for unit := 0; unit < maxEpairUnit; unit++ {
		if !taken[unit] {
			return "epair" + strconv.Itoa(unit), nil
		}
	}
	return "", common.NewAppError(http.StatusInsufficientStorage, fmt.Sprintf(
		"every epair unit below %d is in use or claimed by a jail configuration", maxEpairUnit))
}

// unitsInConfigs reads the epair units the jail configurations already name.
//
// A textual scan rather than a parse: the unit appears in `ifconfig epair7
// create`, in `vnet.interface = "epair7b"` and in the poststop line, and any
// one of them is enough to mark it taken. Reading too much here costs an
// unused unit; reading too little costs a collision.
func (a *Adapter) unitsInConfigs() (map[int]bool, error) {
	claimed := map[int]bool{}

	files, err := filepath.Glob(filepath.Join(a.confDir, "*.conf"))
	if err != nil {
		return nil, common.NewAppError(http.StatusInternalServerError,
			"could not read "+a.confDir+": "+err.Error())
	}
	if a.confFile != "" {
		files = append(files, a.confFile)
	}

	for _, file := range files {
		content, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			// /etc/jail.conf is absent on a host that keeps one file per jail,
			// which is the layout Jail Deck writes (JD-030).
			continue
		}
		if err != nil {
			return nil, common.NewAppError(http.StatusInternalServerError,
				"could not read "+file+": "+err.Error())
		}
		for _, match := range epairUnitRE.FindAllStringSubmatch(string(content), -1) {
			if unit, err := strconv.Atoi(match[1]); err == nil {
				claimed[unit] = true
			}
		}
	}
	return claimed, nil
}

// jailDescr is what a generated VNET stanza writes on the host end of the
// epair, and the only thing that identifies it as belonging to this jail.
func jailDescr(name string) string { return "jail:" + name }

// RemoveJailEpair destroys the interface described as jail:<name>.
//
// One `ifconfig -a` for the whole host rather than a command per interface
// (#2), and the match is on the *description*: an interface is only taken
// away when it says which jail it belongs to. Matching on a name pattern
// could destroy an epair somebody else on the host created.
//
// Destroying the host end is the whole cleanup. Measured on 15.1-RELEASE-p5:
// it takes the jail end and the bridge membership with it, so there is no
// deletem to run first and no bridge name to look up -- and no second
// command that could fail after the first succeeded.
func (a *Adapter) RemoveJailEpair(ctx context.Context, name string) (string, error) {
	result, err := a.runHost(ctx, system.Command{
		Name:    "ifconfig",
		Args:    []string{"-a"},
		Timeout: ifconfigTimeout,
	})
	if err != nil {
		return "", common.NewAppError(http.StatusInternalServerError,
			"could not read the host's interfaces: "+err.Error())
	}

	iface := interfaceDescribed(result.Stdout, jailDescr(name))
	if iface == "" {
		return "", nil
	}

	if _, err := a.runHost(ctx, system.Command{
		Name:    "ifconfig",
		Args:    []string{iface, "destroy"},
		Timeout: ifconfigTimeout,
	}); err != nil {
		return "", common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not destroy %s: %s", iface, err))
	}
	return iface, nil
}

// interfaceDescribed finds the interface carrying descr in `ifconfig -a`
// output, or "".
//
// ifconfig prints an interface's name at the start of a line and its
// settings indented beneath, so the current interface is whatever the last
// unindented line named.
func interfaceDescribed(output, descr string) string {
	current := ""
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			name, _, found := strings.Cut(line, ":")
			if !found {
				// Not an interface header; nothing below it belongs to a
				// previous one either.
				current = ""
				continue
			}
			current = name
			continue
		}
		if strings.TrimSpace(line) == "description: "+descr {
			return current
		}
	}
	return ""
}

var (
	_ jails.EpairAllocator = (*Adapter)(nil)
	_ jails.EpairCleaner   = (*Adapter)(nil)
	_ jails.BridgeSource   = (*Adapter)(nil)
)
