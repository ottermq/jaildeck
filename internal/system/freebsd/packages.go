package freebsd

import (
	"context"
	"time"

	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/templates"
)

// Install runs pkg against a chroot rather than a jail.
//
// -c chroots, so this works on a template that has never been started --
// verified on the deployment target, where
// `pkg -c /usr/local/jails/templates/15.1-RELEASE info` exits 0. It is the same
// shape of operation as JD-007 stage 1's `freebsd-update -b <root>`, which
// already runs against an unbooted template root.
//
// It is not the only way to fill a template, and not the general one: anything
// beyond installing packages is done by configuring a jail and cloning it,
// which is Handbook 17.4.3 (JD-027). This is the path that needs no jail.
//
// -y because there is no terminal to answer a prompt on, and a package
// manager waiting for input is a request that never returns.
func (a *Adapter) Install(ctx context.Context, root string, packages []string) (templates.Result, error) {
	args := append([]string{"-c", root, "install", "-y"}, packages...)
	cmd := system.Command{Name: "pkg", Args: args, Timeout: packageTimeout}
	_, err := a.runHost(ctx, cmd)
	// The command is returned even on failure: which package pkg could not
	// find is the thing an operator needs out of the audit log.
	return templates.Result{Root: root, Command: cmd.String()}, err
}

// packageTimeout is generous: pkg fetches from a mirror and a slow link is not
// a failure. It is still bounded, because a request that never returns is
// worse than one that gives up.
const packageTimeout = 15 * time.Minute

var _ templates.PackageSystem = (*Adapter)(nil)
