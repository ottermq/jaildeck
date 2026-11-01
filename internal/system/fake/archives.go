package fake

import (
	"context"
	"time"

	"github.com/ottermq/jaildeck/internal/housekeeping"
)

// Archives is the fake host's downloads: the release base.txz the fake's
// 15.1-RELEASE template came from, and the debian-13 image's archive, at the
// sizes isengard's are.
type Archives struct{}

func NewArchives() *Archives { return &Archives{} }

func (*Archives) Archives(context.Context) ([]housekeeping.Archive, error) {
	return []housekeeping.Archive{
		{Path: "/usr/local/jails/media/15.1-RELEASE/base.txz", Size: 164624792,
			Modified: time.Date(2026, 6, 12, 8, 28, 15, 0, time.UTC), For: "15.1-RELEASE"},
		{Path: "/zroot/vm/.images/debian-13-genericcloud-amd64.tar.xz", Size: 227978260,
			Modified: time.Date(2026, 10, 26, 16, 10, 0, 0, time.UTC), For: "the debian-13 image"},
	}, nil
}
