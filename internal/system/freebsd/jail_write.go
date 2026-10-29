package freebsd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/jails"
	"github.com/ottermq/jaildeck/internal/system"
)

// confFileMode is what the existing files in /etc/jail.conf.d carry on the
// deployment host: readable, root-writable. A jail's stanza holds no secret —
// the jail's path, its hostname and its address — and making this one 0600
// would only make it differ from its neighbours.
const confFileMode = 0o644

// WriteConfig writes a jail's stanza to <confDir>/<name>.conf.
//
// Plain file I/O rather than a command. The adapter already *reads* this
// directory directly to find configured jails (JD-004), and routing a write
// through sh -c 'cat > file' would put the content on a command line for no
// gain — including in the audit log, which records the command.
//
// O_EXCL: a file that is already there is not overwritten. Jail Deck writes
// this file once, when the jail is created (JD-030), so an existing one was
// written by somebody else and may well be a jail that is running.
func (a *Adapter) WriteConfig(ctx context.Context, name, content string) (string, error) {
	path, err := a.confPath(name)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(a.confDir, 0o755); err != nil {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not create %s: %s", a.confDir, err))
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, confFileMode)
	if os.IsExist(err) {
		return path, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s already exists; Jail Deck will not overwrite a jail configuration it did not just write", path))
	}
	if err != nil {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not create %s: %s", path, err))
	}
	defer file.Close()

	if _, err := file.WriteString(content); err != nil {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not write %s: %s", path, err))
	}
	return path, nil
}

// sysrcTimeout bounds an rc.conf edit. It reads and rewrites one small file
// (JD-014).
const sysrcTimeout = 30 * time.Second

// SetAutostart adds the jail to rc.conf's jail_list, or takes it out again.
//
//	sysrc jail_list+=<name>
//	sysrc jail_list-=<name>
//
// sysrc rather than editing /etc/rc.conf, so the operator's comments and
// ordering survive. Both forms are idempotent — measured: adding a name twice
// and removing one that was never there each change nothing and exit 0 — so
// this needs no read of the current list to be safe to repeat.
func (a *Adapter) SetAutostart(ctx context.Context, name string, enabled bool) (string, error) {
	operator := "-="
	if enabled {
		operator = "+="
	}
	cmd := system.Command{
		Name:    "sysrc",
		Args:    []string{"jail_list" + operator + name},
		Timeout: sysrcTimeout,
	}
	_, err := a.runHost(ctx, cmd)
	return cmd.String(), err
}

// confPath is where a jail's own file goes, and the one place a jail name
// becomes a filesystem path.
//
// The name reaches here from a URL. NewJailName accepts a dot because jls and
// jail.conf do, so "." and ".." arrive intact -- and a path separator would
// escape confDir outright. Neither can name a real jail, so neither is worth
// anything but a refusal.
func (a *Adapter) confPath(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", common.NewAppError(http.StatusBadRequest, fmt.Sprintf(
			"%q cannot name a jail configuration file", name))
	}
	return filepath.Join(a.confDir, name+".conf"), nil
}

// ConfigFile reports where the jail's own file would be and whether it is
// there.
//
// Absent means the jail is configured somewhere Jail Deck does not write --
// /etc/jail.conf, or nowhere at all for a jail started by hand. The caller
// decides what that means; this only answers.
func (a *Adapter) ConfigFile(_ context.Context, name string) (string, bool, error) {
	path, err := a.confPath(name)
	if err != nil {
		return "", false, err
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return path, true, nil
	case os.IsNotExist(err):
		return path, false, nil
	default:
		return path, false, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not read %s: %s", path, err))
	}
}

// RemoveConfig deletes the jail's own file, which is what unconfigures the
// jail: a configured jail is discovered from its configuration (JD-004), so
// once this file is gone the jail is gone from the listing.
//
// A file that is already absent is not an error. ConfigFile is asked first
// and refuses the destroy then; by this point the file was there a moment ago
// and somebody removing it in between has done the same thing this would.
func (a *Adapter) RemoveConfig(_ context.Context, name string) (string, error) {
	path, err := a.confPath(name)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not remove %s: %s", path, err))
	}
	return path, nil
}

// ReadConfig returns the jail's own stanza as it is on disk.
//
// Plain file I/O, as WriteConfig is, and for the same reason: the adapter
// already reads this directory directly to find configured jails (JD-004).
// What the content means is the domain's business -- the comparison that
// needs this generates the file it is held against (JD-016).
func (a *Adapter) ReadConfig(_ context.Context, name string) (string, error) {
	path, err := a.confPath(name)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not read %s: %s", path, err))
	}
	return string(content), nil
}

// ReplaceConfig rewrites a jail's own stanza, and only while it still says
// old (#119).
//
// Written beside it under a name no reader takes -- jail(8)'s .include and
// the discovery glob both match *.conf -- and renamed over it, so a reader
// sees the old file or the new one and never half of either. The mode is the
// file's own, which an operator may have changed. The compare and the rename
// are not one step, so an edit landing between them is lost; a jail's limits
// changed through the API while its stanza is being edited by hand is the
// whole of that window.
func (a *Adapter) ReplaceConfig(_ context.Context, name, old, content string) (string, error) {
	path, err := a.confPath(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not read %s: %s", path, err))
	}
	have, err := os.ReadFile(path)
	if err != nil {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not read %s: %s", path, err))
	}
	if string(have) != old {
		return path, common.NewAppError(http.StatusConflict, fmt.Sprintf(
			"%s changed while Jail Deck was changing it, and it will not overwrite that change; "+
				"read the jail again and repeat the request", path))
	}

	temp := path + ".jaildeck.tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not create %s: %s", temp, err))
	}
	_, writeErr := file.WriteString(content)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(temp, path)
	}
	if writeErr != nil {
		os.Remove(temp)
		return path, common.NewAppError(http.StatusInternalServerError, fmt.Sprintf(
			"could not write %s: %s", path, writeErr))
	}
	return path, nil
}

// Autostarts reports whether rc.conf's jail_list names this jail.
//
//	sysrc -n jail_list
//
// A host that has never set jail_list answers "unknown variable 'jail_list'"
// and exits 1 -- measured. That is an empty list, not a failure to read:
// every jail on such a host is one that does not come back after a reboot,
// which is an answer.
func (a *Adapter) Autostarts(ctx context.Context, name string) (bool, error) {
	result, err := a.runHost(ctx, system.Command{
		Name:    "sysrc",
		Args:    []string{"-n", "jail_list"},
		Timeout: sysrcTimeout,
	})
	if err != nil {
		if exited(result, err) && strings.Contains(result.Stderr, "unknown variable") {
			return false, nil
		}
		return false, err
	}
	return slices.Contains(strings.Fields(result.Stdout), name), nil
}

var (
	_ jails.ConfigWriter    = (*Adapter)(nil)
	_ jails.ConfigRemover   = (*Adapter)(nil)
	_ jails.ConfigReader    = (*Adapter)(nil)
	_ jails.ConfigReplacer  = (*Adapter)(nil)
	_ jails.Autostarter     = (*Adapter)(nil)
	_ jails.AutostartSource = (*Adapter)(nil)
)
