package freebsd

import (
	"context"
	"errors"

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

func (a *Adapter) Start(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("start jail is not implemented for FreeBSD yet")
}

func (a *Adapter) Stop(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("stop jail is not implemented for FreeBSD yet")
}

func (a *Adapter) Restart(ctx context.Context, name string) (domain.Jail, error) {
	return domain.Jail{}, errors.New("restart jail is not implemented for FreeBSD yet")
}
