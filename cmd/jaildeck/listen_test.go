package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func socketPath(t *testing.T) string {
	t.Helper()
	// Short: a unix socket path is capped around 104 bytes on BSD and t.TempDir
	// paths are long, so a nested name can overflow sun_path and fail for a
	// reason that has nothing to do with the test.
	dir, err := os.MkdirTemp("", "jd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func TestListenUnixCreatesSocketAt0660(t *testing.T) {
	path := socketPath(t)
	l, err := listenUnix(path, "")
	if err != nil {
		t.Fatalf("listenUnix() error = %v", err)
	}
	defer l.Close()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("the socket was not created: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("%s is %s, want a socket", path, info.Mode())
	}
	// The mode is set by umask at creation, not by a chmod afterwards, so there
	// is no window in which the socket is world-connectable.
	if perm := info.Mode().Perm(); perm != socketMode {
		t.Errorf("mode = %04o, want %04o", perm, socketMode)
	}
}

func TestListenUnixSetsTheGroup(t *testing.T) {
	// Whichever group this process already belongs to: the test asserts that
	// the lookup and chown path works, not that a particular group exists.
	gids, err := os.Getgroups()
	if err != nil || len(gids) == 0 {
		t.Skip("no supplementary groups to test against")
	}
	g, err := user.LookupGroupId(strconv.Itoa(gids[len(gids)-1]))
	if err != nil {
		t.Skipf("gid %d has no name on this system: %v", gids[len(gids)-1], err)
	}

	path := socketPath(t)
	l, err := listenUnix(path, g.Name)
	if err != nil {
		t.Fatalf("listenUnix(group=%q) error = %v", g.Name, err)
	}
	defer l.Close()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := statGid(t, info); got != g.Gid {
		t.Errorf("socket gid = %s, want %s (%s)", got, g.Gid, g.Name)
	}
}

func TestListenUnixRejectsAnUnknownGroup(t *testing.T) {
	path := socketPath(t)
	l, err := listenUnix(path, "definitely-not-a-group-on-this-host")
	if err == nil {
		l.Close()
		t.Fatal("listenUnix() with an unknown group returned no error")
	}
	if !strings.Contains(err.Error(), "socket group") {
		t.Errorf("error = %q, want it to name the socket group", err)
	}
	// And it must not leave a socket behind that nothing is serving.
	if _, statErr := os.Lstat(path); statErr == nil {
		t.Errorf("%s still exists after the failure", path)
	}
}

// A socket left by a process that did not shut down cleanly must not stop the
// next start: binding over it fails with EADDRINUSE whether or not anything is
// listening.
func TestListenUnixReplacesAStaleSocket(t *testing.T) {
	path := socketPath(t)
	first, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	// Closing the listener without removing the file is what a kill -9 leaves.
	first.(*net.UnixListener).SetUnlinkOnClose(false)
	first.Close()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("expected a leftover socket file: %v", err)
	}

	l, err := listenUnix(path, "")
	if err != nil {
		t.Fatalf("listenUnix() over a stale socket error = %v", err)
	}
	l.Close()
}

// But a socket something is still serving is another instance, and taking it
// over would silently steal its clients.
func TestListenUnixRefusesALiveSocket(t *testing.T) {
	path := socketPath(t)
	live, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	l, err := listenUnix(path, "")
	if err == nil {
		l.Close()
		t.Fatal("listenUnix() took over a socket another process is serving")
	}
	if !strings.Contains(err.Error(), "already being served") {
		t.Errorf("error = %q, want it to say the socket is in use", err)
	}
}

func TestListenUnixRefusesANonSocketPath(t *testing.T) {
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := listenUnix(path, "")
	if err == nil {
		l.Close()
		t.Fatal("listenUnix() returned no error for a regular file")
	}
	if !strings.Contains(err.Error(), "not a socket") {
		t.Errorf("error = %q, want it to say the path is not a socket", err)
	}
	// The file is the operator's, not ours to delete.
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the regular file was removed: %v", err)
	}
}

// The acceptance criterion: both listeners serve the same router. One
// http.Server over both is what guarantees it, so the test drives each.
func TestBothListenersServeTheSameHandler(t *testing.T) {
	path := socketPath(t)
	unix, err := listenUnix(path, "")
	if err != nil {
		t.Fatal(err)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := newServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("same-handler:" + r.URL.Path))
	}))
	for _, l := range []net.Listener{unix, tcp} {
		go srv.Serve(l)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	overUnix := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	for name, c := range map[string]struct {
		client *http.Client
		url    string
	}{
		"unix": {overUnix, "http://socket/api/jails"},
		"tcp":  {http.DefaultClient, "http://" + tcp.Addr().String() + "/api/jails"},
	} {
		resp, err := c.client.Get(c.url)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "same-handler:/api/jails" {
			t.Errorf("%s served %q, want the same handler", name, body)
		}
	}
}

// statGid reads the socket's group, which is not in os.FileInfo's portable
// surface. Unix-only, like the rest of this file.
func statGid(t *testing.T, info os.FileInfo) string {
	t.Helper()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("FileInfo.Sys() is not a syscall.Stat_t on this platform")
	}
	return strconv.FormatUint(uint64(st.Gid), 10)
}
