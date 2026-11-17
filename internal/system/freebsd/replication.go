package freebsd

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ottermq/jaildeck/internal/replication"
	"github.com/ottermq/jaildeck/internal/system"
)

// cursorName is zrepl's replication cursor bookmark: <fs>#zrepl_CURSOR_G_
// <snapshot guid>_J_<job> (#247). The older unversioned
// zrepl_replication_cursor, from before zrepl 0.3, names no job and is not
// read.
var cursorName = regexp.MustCompile(`^([^#@\s]+)#zrepl_CURSOR_G_[0-9a-f]+_J_([A-Za-z0-9._:-]{1,128})$`)

// ReplicationCursors lists zrepl's replication cursors on every pool, with
// the creation time of the snapshot each one marks. One `zfs list` of
// bookmarks: measured at 0.03 s where listing every snapshot took 2.8 s.
func (a *Adapter) ReplicationCursors(ctx context.Context) ([]replication.Cursor, error) {
	result, err := a.runHost(ctx, system.Command{
		Name: "zfs", Args: []string{"list", "-H", "-p", "-t", "bookmark", "-o", "name,creation"},
		Timeout: metricsTimeout, MaxOutput: 8 << 20,
	})
	if err != nil {
		return nil, err
	}
	if result.StdoutTruncated {
		return nil, errors.New("zfs list of bookmarks was longer than Jail Deck reads")
	}
	cursors := []replication.Cursor{}
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		name, created, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		m := cursorName.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		seconds, err := strconv.ParseInt(strings.TrimSpace(created), 10, 64)
		if err != nil {
			continue
		}
		cursors = append(cursors, replication.Cursor{Dataset: m[1], Job: m[2], Snapshot: time.Unix(seconds, 0)})
	}
	return cursors, nil
}
