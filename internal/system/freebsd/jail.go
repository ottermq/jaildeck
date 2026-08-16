package freebsd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ottermq/jaildeck/internal/jails"
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

func (a *Adapter) List(ctx context.Context) ([]jails.Jail, error) {
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

func (a *Adapter) runService(ctx context.Context, name, action string) (jails.Jail, error) {
	cmd := system.Command{
		Name: "service",
		Args: []string{"jail", action, name},
	}

	result, runErr := a.runner.Run(ctx, cmd)
	jail, stateErr := a.getJailByName(ctx, name)
	cmdErr := &system.CommandError{
		Command: cmd.Name,
		Args:    cmd.Args,
		Result:  result,
		Err:     stateErr,
	}

	summary := summarizeJailActionFailure(name, action, result)
	if stateErr != nil {
		if runErr != nil {
			cmdErr.Err = runErr
			return jails.Jail{}, cmdErr
		}
		if summary != "" {
			cmdErr.Err = errors.New(summary)
		}
		return jails.Jail{}, cmdErr
	}
	if runErr != nil {
		cmdErr.Err = runErr
		return jail, cmdErr
	}
	want := desiredStatusForAction(action)

	if jail.Status != want {
		cmdErr.Err = fmt.Errorf("%s: jail status is '%s', want '%s'", summary, jail.Status, want)
		return jail, cmdErr
	}

	return jail, nil
}

func desiredStatusForAction(action string) jails.JailStatus {
	switch action {
	case "start", "restart":
		return jails.JailStatusRunning
	case "stop":
		return jails.JailStatusStopped
	default:
		return ""
	}
}

func (a *Adapter) Start(ctx context.Context, name string) (jails.Jail, error) {
	return a.runService(ctx, name, "start")
}

func (a *Adapter) Stop(ctx context.Context, name string) (jails.Jail, error) {
	return a.runService(ctx, name, "stop")
}

func (a *Adapter) Restart(ctx context.Context, name string) (jails.Jail, error) {
	return a.runService(ctx, name, "restart")
}

func summarizeJailActionFailure(name, action string, result system.CommandResult) string {
	output := strings.TrimSpace(result.Stderr)
	if output == "" {
		output = strings.TrimSpace(result.Stdout)
	}

	output = strings.ReplaceAll(output, "\n", " ")
	output = strings.Join(strings.Fields(output), " ")

	prefixes := []string{
		"Starting jails:",
		"Stopping jails:",
		fmt.Sprintf("cannot start jail \"%s\":", name),
		fmt.Sprintf("cannot start jail  \"%s\":", name),
		fmt.Sprintf("jail: %s:", name),
		fmt.Sprintf("jail: \"%s\"", name),
	}

	for _, prefix := range prefixes {
		output = strings.ReplaceAll(output, prefix, "")
	}

	output = strings.Join(strings.Fields(output), " ")
	output = strings.Trim(output, ".: ")

	if output == "" {
		return fmt.Sprintf("%s jail %q failed", action, name)
	}

	return output
}
