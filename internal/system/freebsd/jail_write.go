package freebsd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	path := filepath.Join(a.confDir, name+".conf")

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

var (
	_ jails.ConfigWriter = (*Adapter)(nil)
	_ jails.Autostarter  = (*Adapter)(nil)
)
