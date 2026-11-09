package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ottermq/jaildeck/internal/app"
	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/internal/networks"
)

// runNetworks is `jaildeck networks restore`: the boot step that puts back
// the tenant networks' bridges a reboot took (JD-059), before jails and
// guests start. Like init, it builds its own adapter and service rather than
// going through app.New: it runs at boot, with no service up, and needs no
// credential.
func runNetworks(subArgs []string, out, errOut io.Writer, cfg *config.Config) error {
	if len(subArgs) != 1 || subArgs[0] != "restore" {
		return fmt.Errorf("%w: %v (want restore)", ErrUnexpectedArgs, subArgs)
	}
	sys, err := app.NewNetworkSystem(cfg.Adapter)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.AuditLog), 0o700); err != nil {
		return withPermissionHint(fmt.Errorf("creating audit log directory: %w", err))
	}
	service := networks.NewService(sys, audit.NewFileLogger(cfg.AuditLog), app.NetworkSettings(cfg))
	done, err := service.Restore(context.Background())
	printRestored(out, errOut, done)
	return err
}

func printRestored(out, errOut io.Writer, done networks.Restored) {
	for _, r := range done.Created {
		fmt.Fprintf(out, "restored %s: the network %s, gateway %s\n", r.Bridge, r.Name, r.Gateway)
	}
	for _, r := range done.Present {
		fmt.Fprintf(out, "%s is there: the network %s\n", r.Bridge, r.Name)
	}
	for _, p := range done.Problems {
		fmt.Fprintln(errOut, p)
	}
	if len(done.Created)+len(done.Present)+len(done.Problems) == 0 {
		fmt.Fprintln(out, "no tenant networks are recorded: nothing to do")
	}
}
