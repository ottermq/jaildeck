package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/host"
	"github.com/ottermq/jaildeck/internal/system"
	"github.com/ottermq/jaildeck/internal/system/fake"
	"github.com/ottermq/jaildeck/internal/system/freebsd"
)

// runInit ensures the jails dataset layout. It builds its own adapter, service
// and audit logger rather than going through app.New, which refuses to start
// without an operator credential: init may run before one exists.
func runInit(subArgs []string, out, errOut io.Writer, cfg *config.Config) error {
	if len(subArgs) > 0 {
		return fmt.Errorf("%w: %v", ErrUnexpectedArgs, subArgs)
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
