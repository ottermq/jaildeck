package freebsd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/system"
)

func TestReplicationCursors(t *testing.T) {
	// zfs list -H -p -t bookmark -o name,creation, with zrepl's cursors and
	// what else lives among bookmarks.
	r := &auditRunner{result: system.CommandResult{Stdout: "" +
		"tank/jails/containers/db#zrepl_CURSOR_G_ed671b640f7dab85_J_local_push\t1794942041\n" +
		"tank/offsite#zrepl_CURSOR_G_c0d59ea313d5cfc6_J_offsite_source\t1794930970\n" +
		// Someone's own bookmark, and zrepl's pre-0.3 cursor naming no job.
		"tank/jails#before-upgrade\t1794000000\n" +
		"tank/old#zrepl_replication_cursor\t1790000000\n" +
		"tank/x#zrepl_CURSOR_G_abc_J_push\tnot-a-number\n"}}
	got, err := auditAdapter(r).ReplicationCursors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Dataset != "tank/jails/containers/db" || got[0].Job != "local_push" || !got[0].Snapshot.Equal(time.Unix(1794942041, 0)) {
		t.Errorf("first %+v", got[0])
	}
	if got[1].Job != "offsite_source" {
		t.Errorf("second %+v", got[1])
	}
	if cmd := r.ran[0].String(); cmd != "zfs list -H -p -t bookmark -o name,creation" {
		t.Errorf("ran %q", cmd)
	}
}

func TestReplicationCursorsFailures(t *testing.T) {
	for name, r := range map[string]*auditRunner{
		"zfs failing": {result: system.CommandResult{ExitCode: 1, Stderr: "internal error"}, err: errors.New("exit status 1")},
		"truncated":   {result: system.CommandResult{Stdout: "a#b\t1\n", StdoutTruncated: true}},
	} {
		if _, err := auditAdapter(r).ReplicationCursors(context.Background()); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
