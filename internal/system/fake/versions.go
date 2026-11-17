package fake

import (
	"context"
	"errors"

	"github.com/ottermq/jaildeck/internal/osversion"
)

// Versions is a host that has installed an update and not been restarted,
// with a jail and a template behind it and one jail with no path, so the
// interface has every version state to show without a FreeBSD box (#229).
// Anything created while it runs is current.
type Versions struct{}

func NewVersions() Versions { return Versions{} }

func (Versions) HostVersions(context.Context) (osversion.Host, error) {
	return osversion.NewHost("15.1-RELEASE-p5", "15.1-RELEASE-p6", "15.1-RELEASE-p6"), nil
}

var fakeUserlands = map[string]string{
	"/fake/jail/nginx":                  "15.1-RELEASE-p6",
	"/fake/jail/redis":                  "15.1-RELEASE-p2",
	"/fake/jail/templates/15.1-RELEASE": "15.1-RELEASE-p5",
}

func (Versions) Userland(_ context.Context, root string) (string, error) {
	if root == "" {
		return "", errors.New("no root")
	}
	if v, ok := fakeUserlands[root]; ok {
		return v, nil
	}
	return "15.1-RELEASE-p6", nil
}

var _ osversion.Source = Versions{}
