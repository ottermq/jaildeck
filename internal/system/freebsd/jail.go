package freebsd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

func (a *Adapter) listConfiguredJails() ([]domain.Jail, error) {
	mapConfigJails := make(map[string]bool)
	dirJails, err := listJailsFromConfDir(defaultJailConfDir)
	if err != nil {
		return nil, err
	}
	for _, j := range dirJails {
		mapConfigJails[j.Name] = true
	}
	fileJails, err := listJailsFromConfFile(defaultJailConf)
	if err != nil {
		return nil, err
	}
	jails := make([]domain.Jail, 0, len(dirJails)+len(fileJails))
	jails = append(jails, dirJails...)
	for _, j := range fileJails {
		if ok := mapConfigJails[j.Name]; !ok {
			jails = append(jails, j)
		}
	}
	return jails, nil
}

func (a *Adapter) runningJails(ctx context.Context) ([]domain.Jail, error) {
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

func mergeJails(configured, running []domain.Jail) []domain.Jail {
	maxLen := len(configured) + len(running)
	merged := make([]domain.Jail, 0, maxLen)
	runningNotConfigured := make([]domain.Jail, 0, len(running))
	merged = append(merged, configured...)

	for _, running := range running {
		matched := false
		for idx, configured := range merged {
			if configured.Name == running.Name {
				merged[idx] = running
				matched = true
			}
		}
		if matched {
			continue
		}
		runningNotConfigured = append(runningNotConfigured, running)
		log.Printf("[WARN] jail %s not found in configuration files", running.Name)
	}
	merged = append(merged, runningNotConfigured...)
	return merged
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

func (a *Adapter) Start(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("start jail is not implemented for FreeBSD yet")
}

func (a *Adapter) Stop(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("stop jail is not implemented for FreeBSD yet")
}

func (a *Adapter) Restart(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("restart jail is not implemented for FreeBSD yet")
}
