package freebsd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codeberg.org/Otter-Labs/jaildeck/internal/jails"
	"codeberg.org/Otter-Labs/jaildeck/internal/system"
)

// jailOperationTimeout is a backstop, not the primary limit. FreeBSD bounds a
// jail stop itself through exec.timeout and stop.timeout, and it cleans up
// after its own timeouts; killing the command from outside does not. Keep this
// above exec.timeout + stop.timeout so FreeBSD's limits always fire first.
const jailOperationTimeout = 150 * time.Second

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

func (a *Adapter) runService(ctx context.Context, name string, op jails.Operation) (jails.OperationResult, error) {
	cmd := system.Command{
		Name:    "service",
		Args:    []string{"jail", op.String(), name},
		Timeout: jailOperationTimeout,
	}

	result, runErr := a.runner.Run(ctx, cmd)
	jail, stateErr := a.getJailByName(ctx, name)
	cmdErr := &system.CommandError{
		Command: cmd.Name,
		Args:    cmd.Args,
		Result:  result,
		Err:     stateErr,
	}
	opResult := jails.OperationResult{
		Jail:    jail,
		Command: cmd.String(),
	}

	summary := summarizeJailActionFailure(name, op, result)
	if stateErr != nil {
		if runErr != nil {
			cmdErr.Err = runErr
			return opResult, cmdErr
		}
		if summary != "" {
			cmdErr.Err = fmt.Errorf("%s: %w", summary, stateErr)
		}
		return opResult, cmdErr
	}
	if runErr != nil {
		cmdErr.Err = runErr

		return opResult, cmdErr
	}
	want := op.DesiredStatus()

	if jail.Status != want {
		cmdErr.Err = fmt.Errorf("%s: jail status is '%s', want '%s'", summary, jail.Status, want)
		return opResult, cmdErr
	}

	return opResult, nil
}

func (a *Adapter) Start(ctx context.Context, name string) (jails.OperationResult, error) {
	return a.runService(ctx, name, jails.OperationStart)
}

func (a *Adapter) Stop(ctx context.Context, name string) (jails.OperationResult, error) {
	return a.runService(ctx, name, jails.OperationStop)
}

func (a *Adapter) Restart(ctx context.Context, name string) (jails.OperationResult, error) {
	return a.runService(ctx, name, jails.OperationRestart)
}

func summarizeJailActionFailure(name string, op jails.Operation, result system.CommandResult) string {
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
		fmt.Sprintf("jail: %s:", name),
		fmt.Sprintf("jail: \"%s\"", name),
	}

	for _, prefix := range prefixes {
		output = strings.ReplaceAll(output, prefix, "")
	}

	output = strings.Join(strings.Fields(output), " ")
	output = strings.Trim(output, ".: ")

	if output == "" {
		return fmt.Sprintf("%s jail %q failed", op, name)
	}

	return output
}
