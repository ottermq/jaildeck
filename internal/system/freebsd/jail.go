package freebsd

import (
	"context"
	"fmt"
	"strings"

	"github.com/otterlabs/jaildeck/internal/domain"
	"github.com/otterlabs/jaildeck/internal/system"
)

type jlsOutput struct {
	JailInformation struct {
		Jails []jlsJail `json:"jail"`
	} `json:"jail-information"`
}

type jlsJail struct {
	JID      string   `json:"jid"`
	Name     string   `json:"name"`
	Hostname string   `json:"host.hostname"`
	IP4      []string `json:"ip4.addr"`
	Path     string   `json:"path"`
}

var jlsListCommand = system.Command{
	Name: "jls",
	Args: []string{"--libxo=json", "jid", "name", "host.hostname", "ip4.addr", "path"},
}

func (a *Adapter) List(ctx context.Context) ([]domain.Jail, error) {
	configured, err := a.listConfiguredJails()
	if err != nil {
		return nil, err
	}

	running, err := a.runningJails(ctx)
	if err != nil {
		return nil, err
	}

	return mergeJails(configured, running), nil
}

func (a *Adapter) runService(ctx context.Context, name, action string) (domain.Jail, error) {
	cmd := system.Command{
		Name: "service",
		Args: []string{"jail", action, name},
	}
	result, err := a.runner.Run(ctx, cmd)

	if err != nil {
		return domain.Jail{}, fmt.Errorf("%q jail %q: %w", action, name, err)
	}

	if found := strings.Contains(result.Stdout, "cannot"); found {
		return domain.Jail{}, fmt.Errorf(
			"%s jail %q failed: %w; exit=%d stdout=%q stderr=%q",
			action,
			name,
			err,
			result.ExitCode,
			result.Stdout,
			result.Stderr,
		)
	}

	return a.getJailByName(ctx, name)
}

func (a *Adapter) Start(ctx context.Context, name string) (domain.Jail, error) {
	return a.runService(ctx, name, "start")
}

func (a *Adapter) Stop(ctx context.Context, name string) (domain.Jail, error) {
	return a.runService(ctx, name, "stop")
}

func (a *Adapter) Restart(ctx context.Context, name string) (domain.Jail, error) {
	return a.runService(ctx, name, "restart")
}
