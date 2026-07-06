package freebsd

import (
	"context"
	"encoding/json"
	"errors"
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
	result, err := a.runner.Run(ctx, jlsListCommand)
	if err != nil {
		return nil, fmt.Errorf("list jails: %w", err)
	}
	parsedOutput, err := parseJLSOutput(result.Stdout)
	if err != nil {
		return nil, fmt.Errorf("fail parsing jls output: %w", err)
	}
	jails := make([]domain.Jail, len(parsedOutput.JailInformation.Jails))
	for idx, j := range parsedOutput.JailInformation.Jails {
		jails[idx] = domain.Jail{
			JID:      j.JID,
			Name:     j.Name,
			Status:   domain.JailStatusRunning,
			Hostname: j.Hostname,
			IP:       strings.Join(j.IP4, ", "),
			Path:     j.Path,
		}
	}
	return jails, nil
}

func parseJLSOutput(stdout string) (jlsOutput, error) {
	var output jlsOutput
	err := json.Unmarshal([]byte(stdout), &output)
	if err != nil {
		return jlsOutput{}, err
	}
	return output, nil
}

func (a *Adapter) Start(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("start jail is not implemented for FreeBSD yet")
}

func (a *Adapter) Stop(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("stop jail is not implemented for FreeBSD yet")
}

func (a *Adapter) Restart(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("restart jail is not implemented for FreeBSD yet")
}
