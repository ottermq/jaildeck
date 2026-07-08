package freebsd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ottermq/jaildeck/internal/domain"
	"github.com/ottermq/jaildeck/internal/system"
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
	if err == nil {
		err = validateJailActionResult(name, action, result)
	}
	if err != nil {
		jail, _ := a.getJailByName(ctx, name)
		return jail, &system.CommandError{
			Command: cmd.Name,
			Args:    cmd.Args,
			Result:  result,
			Err:     err,
		}
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

func validateJailActionResult(name, action string, result system.CommandResult) error {
	expectedVerbPrefix := "Starting"
	if action == "stop" || action == "restart" {
		expectedVerbPrefix = "Stopping"
	}
	prefix := fmt.Sprintf("%s jails: ", expectedVerbPrefix)

	trimmed, _ := strings.CutPrefix(result.Stdout, prefix)
	trimmed = strings.ReplaceAll(trimmed, "\n", " ")
	trimmed = strings.ReplaceAll(trimmed, "  ", " ")
	trimmed = strings.TrimSpace(trimmed)
	// expected to find if success: "<jail>."
	if retrimmed, ok := strings.CutSuffix(trimmed, fmt.Sprintf("%s.", name)); ok {
		if len(retrimmed) == 0 {
			return nil
		}
	}
	if retrimmed, ok := strings.CutSuffix(trimmed, "."); ok {
		if len(retrimmed) == 0 {
			return nil
		}
	}
	line_prefix := fmt.Sprintf("jail: %s:", name)
	switch action {
	case "start":
		// for the command "start", it is expected "cannot start jail   "<name>":"
		start_prefix := fmt.Sprintf("cannot start jail \"%s\":", name)
		if start_trimmed, ok := strings.CutPrefix(trimmed, start_prefix); ok {
			// means error
			start_trimmed = strings.TrimSpace(start_trimmed)
			startErr, _ := strings.CutPrefix(start_trimmed, line_prefix)
			startErr = strings.ReplaceAll(startErr, line_prefix, "; ")
			return errors.New(strings.TrimSpace(startErr))
		}
	case "stop", "restart":
		// for the command "stop", it is expected the prefix <name>
		// Restart => stop + start
		stop_trimmed, _ := strings.CutPrefix(trimmed, name)
		if len(stop_trimmed) == 1 {
			return nil
		}
		stopErr, _ := strings.CutPrefix(stop_trimmed, line_prefix)
		return errors.New(strings.TrimSpace(stopErr))
	default:
		return errors.New("unknown action " + action)
	}
	return errors.New(trimmed)
}
