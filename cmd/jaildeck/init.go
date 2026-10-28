package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ottermq/jaildeck/internal/app"
	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/networks"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/fake"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
)

// runInit ensures the jails dataset layout, or with `vm-network` the default
// VM network (JD-040). It builds its own adapter, service and audit logger
// rather than going through app.New, which refuses to start without an
// operator credential: init may run before one exists.
func runInit(subArgs []string, out, errOut io.Writer, cfg *config.Config) error {
	if len(subArgs) == 1 && subArgs[0] == "vm-network" {
		return runInitVMNetwork(out, cfg)
	}
	if len(subArgs) > 0 {
		return fmt.Errorf("%w: %v (want nothing, or vm-network)", ErrUnexpectedArgs, subArgs)
	}

	var hostSystem host.HostSystem
	switch cfg.Adapter {
	case config.AdapterFreeBSD:
		hostSystem = freebsd.NewAdapter(system.NewExecCommandRunner())
	case config.AdapterFake:
		hostSystem = fake.NewHostSystem()
	default:
		return fmt.Errorf("unknown JAILDECK_ADAPTER %q (want %q or %q)",
			cfg.Adapter, config.AdapterFreeBSD, config.AdapterFake)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.AuditLog), 0o700); err != nil {
		return withPermissionHint(fmt.Errorf("creating audit log directory: %w", err))
	}
	logger := audit.NewFileLogger(cfg.AuditLog)

	return ensureLayout(context.Background(), host.NewService(hostSystem, logger, cfg.ZFSPool), out, errOut)
}

// ensureLayout is the same service method PUT /api/host/layout calls.
func ensureLayout(ctx context.Context, service *host.Service, out, errOut io.Writer) error {
	change, err := service.EnsureLayout(ctx)
	if err != nil {
		return err
	}
	if !change.Changed {
		fmt.Fprintf(out, "%s/jails is already in place: nothing changed\n", change.Pool)
		return nil
	}
	for _, name := range change.Created {
		fmt.Fprintf(out, "created %s\n", name)
	}
	if len(change.Missing) > 0 {
		// Should not happen: every create succeeded. Say so rather than
		// claim a layout the system does not show.
		fmt.Fprintf(errOut, "still missing after creating: %s\n", strings.Join(change.Missing, ", "))
		return fmt.Errorf("layout on %s is incomplete", change.Pool)
	}
	fmt.Fprintf(out, "%s/jails is in place\n", change.Pool)
	return nil
}

// runInitVMNetwork is the same service method PUT /api/networks/default calls.
func runInitVMNetwork(out io.Writer, cfg *config.Config) error {
	sys, err := app.NewNetworkSystem(cfg.Adapter)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.AuditLog), 0o700); err != nil {
		return withPermissionHint(fmt.Errorf("creating audit log directory: %w", err))
	}
	service := networks.NewService(sys, audit.NewFileLogger(cfg.AuditLog), app.NetworkSettings(cfg))
	ensured, err := service.EnsureDefault(context.Background())
	if err != nil {
		return err
	}
	return printEnsured(out, ensured)
}

func printEnsured(out io.Writer, ensured networks.Ensured) error {
	if !ensured.Changed {
		fmt.Fprintf(out, "%s: nothing to do\n", ensured.Switch)
	}
	for _, id := range ensured.Done {
		fmt.Fprintf(out, "done: %s\n", id)
	}
	if ensured.Note != "" {
		fmt.Fprintln(out, ensured.Note)
	}
	for _, part := range ensured.Parts {
		if part.Status == networks.PartPass {
			continue
		}
		fmt.Fprintf(out, "\n%s -- %s (%s)\n", part.Title, part.Status, part.Owner)
		if part.Detail != "" {
			fmt.Fprintf(out, "  %s\n", part.Detail)
		}
		if part.Remedy != "" {
			fmt.Fprintf(out, "  %s\n", strings.ReplaceAll(part.Remedy, "\n", "\n  "))
		}
	}
	switch {
	case ensured.Ready:
		fmt.Fprintf(out, "\nthe default VM network %s (%s) is ready\n", ensured.Switch, ensured.Subnet)
	case ensured.Usable:
		fmt.Fprintf(out, "\nguests on %s reach the host; the rest above is needed for the outside\n", ensured.Switch)
	}
	return nil
}
