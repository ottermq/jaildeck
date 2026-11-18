package freebsd

import (
	"context"
	"slices"
	"strings"
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

// Upgrade upgrades every package in a template's root to the repository's
// current version (#252), the same way Install installs: pkg -c, so nothing
// is started. Only ever run on a tree Jail Deck built itself -- the refresh
// refuses any other -- because pkg runs as root and executes the packages'
// own scripts inside the chroot.
//
// Measured on FreeBSD 15.1 against a derived PostgreSQL template: two
// packages upgraded in 10 s, after a 25 s catalogue update.
func (a *Adapter) Upgrade(ctx context.Context, root string) (templates.Result, error) {
	cmd := system.Command{Name: "pkg", Args: []string{"-c", root, "upgrade", "-y"}, Timeout: packageTimeout}
	_, err := a.runHost(ctx, cmd)
	return templates.Result{Root: root, Command: cmd.String()}, err
}

// Explicit names the packages installed in root on purpose (#264).
func (a *Adapter) Explicit(ctx context.Context, root string) ([]string, error) {
	return a.queryPackages(ctx, root, "%a == 0")
}

// Orphans names the packages in root installed as a dependency that nothing
// depends on any more (#264): what pkg autoremove would take.
func (a *Adapter) Orphans(ctx context.Context, root string) ([]string, error) {
	return a.queryPackages(ctx, root, "%a == 1 && %#r == 0")
}

func (a *Adapter) queryPackages(ctx context.Context, root, expression string) ([]string, error) {
	cmd := system.Command{Name: "pkg", Args: []string{"-c", root, "query", "-e", expression, "%n"}, Timeout: queryTimeout}
	result, err := a.runHost(ctx, cmd)
	if err != nil {
		return nil, err
	}
	names := strings.Fields(result.Stdout)
	slices.Sort(names)
	return names, nil
}

// MarkExplicit marks pkg as installed on purpose again (#264). One package a
// call, so the audit log names each one.
func (a *Adapter) MarkExplicit(ctx context.Context, root, pkg string) (templates.Result, error) {
	cmd := system.Command{Name: "pkg", Args: []string{"-c", root, "set", "-y", "-A", "0", pkg}, Timeout: queryTimeout}
	_, err := a.runHost(ctx, cmd)
	return templates.Result{Root: root, Command: cmd.String()}, err
}

// queryTimeout bounds a local pkg database read or flag change: no mirror.
const queryTimeout = time.Minute

// packageTimeout is generous: pkg fetches from a mirror and a slow link is not
// a failure. It is still bounded, because a request that never returns is
// worse than one that gives up.
const packageTimeout = 15 * time.Minute

var _ templates.PackageSystem = (*Adapter)(nil)
