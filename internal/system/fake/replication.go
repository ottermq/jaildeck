package fake

import (
	"context"
	"time"

	"github.com/ottermq/jaildeck/internal/replication"
)

// Replication is a host with two zrepl jobs, so the interface has a current
// one and one falling behind without a FreeBSD box (#247): a push every 15
// minutes to a local backup pool, and a source another host pulls from that
// has not pulled for a few hours.
type Replication struct{ now func() time.Time }

func NewReplication() Replication { return Replication{now: time.Now} }

func (r Replication) ReplicationCursors(context.Context) ([]replication.Cursor, error) {
	now := r.now().Truncate(time.Minute)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	return []replication.Cursor{
		{Dataset: "zroot/jails", Job: "local_push", Snapshot: ago(9 * time.Minute)},
		{Dataset: "zroot/jails/nginx", Job: "local_push", Snapshot: ago(9 * time.Minute)},
		{Dataset: "zroot/jails/nginx/logs", Job: "local_push", Snapshot: ago(24 * time.Minute)},
		{Dataset: "zroot/jails/redis", Job: "local_push", Snapshot: ago(9 * time.Minute)},
		{Dataset: "zroot/jails/redis", Job: "offsite", Snapshot: ago(5*time.Hour + 12*time.Minute)},
	}, nil
}
