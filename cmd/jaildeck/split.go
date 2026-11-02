package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ottermq/jaildeck/internal/app"
	"github.com/ottermq/jaildeck/internal/auth"
	"github.com/ottermq/jaildeck/internal/buildinfo"
	"github.com/ottermq/jaildeck/internal/config"
	"github.com/ottermq/jaildeck/web"
)

// Jail Deck started as root runs as two processes (JD-056). This one, the
// root side, serves the API on a socket in the run directory, and starts the
// front as the jaildeck user: the listeners, the UI and signing in, with
// the root side reached through that socket.
//
// The root side opens every listener the front serves on and hands them
// over, so that the front binds nothing and the operator's socket keeps the
// owner and mode it always had. The front gets, as descriptors:
//
//	3      the read end of a pipe the root side never writes to: its end
//	       closing is how the front learns the root side has gone
//	4      the socket the root side asks whether a login still holds
//	5...   the listeners, as many as JAILDECK_FRONT_LISTENERS says
const (
	frontListenersEnv = "JAILDECK_FRONT_LISTENERS"
	apiSocketName     = "api.sock"
	recheckSocketName = "front.sock"
	stateFileName     = "state.json"
)

// frontLifeline is the write end of the front's pipe. Held here so that it
// is never collected, which would close it and stop the front.
var frontLifeline *os.File

func runSplit(cfg *config.Config, account *user.User) error {
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("the user %s has no numeric uid: %w", account.Username, err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("the user %s has no numeric gid: %w", account.Username, err)
	}
	group, err := user.LookupGroupId(account.Gid)
	if err != nil {
		return fmt.Errorf("the user %s's group: %w", account.Username, err)
	}

	if err := app.RequireCredential(cfg); err != nil {
		return err
	}
	if err := shareStateDir(cfg.StateDir, gid); err != nil {
		return err
	}
	if err := prepareRunDir(cfg.RunDir, gid); err != nil {
		return err
	}
	apiListener, err := listenUnix(filepath.Join(cfg.RunDir, apiSocketName), group.Name)
	if err != nil {
		return err
	}
	recheckPath := filepath.Join(cfg.RunDir, recheckSocketName)
	recheckListener, err := listenUnix(recheckPath, "")
	if err != nil {
		return err
	}
	front := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", recheckPath)
		},
	}}
	back, err := app.NewBack(cfg, auth.RemoteRecheck(front))
	if err != nil {
		return err
	}
	public, err := openListeners(cfg)
	if err != nil {
		return err
	}
	files, err := listenerFiles(append([]net.Listener{recheckListener}, public...))
	if err != nil {
		return err
	}
	slog.Info("listening", "version", buildinfo.Version(), "on", listenerNames(public),
		"front_user", account.Username, "api", apiListener.Addr().String())

	go superviseFront(uid, gid, len(public), files)
	return newServer("", back.Routes()).Serve(apiListener)
}

// shareStateDir makes the state directory the front's to read: root's,
// in the front's group, 0750, and the state file 0640. Every start does it,
// which is also the upgrade from a Jail Deck that ran as one process. A
// directory holding anything but the state file is refused rather than
// opened to a group: it would not be Jail Deck's alone.
func shareStateDir(dir string, gid int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading the state dir: %w", err)
	}
	for _, e := range entries {
		if e.Name() != stateFileName {
			return fmt.Errorf("the state dir %s holds %s as well as Jail Deck's %s; give Jail Deck a directory of its own",
				dir, e.Name(), stateFileName)
		}
	}
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0o750}, {filepath.Join(dir, stateFileName), 0o640}} {
		if err := os.Lchown(p.path, 0, gid); err != nil {
			return fmt.Errorf("sharing %s with the front: %w", p.path, err)
		}
		if err := os.Chmod(p.path, p.mode); err != nil {
			return fmt.Errorf("sharing %s with the front: %w", p.path, err)
		}
	}
	return nil
}

// prepareRunDir makes the directory the sockets live in: root's, in the
// front's group, 0750, so that the front reaches the API socket and nobody
// else reaches either.
func prepareRunDir(dir string, gid int) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if err := os.Chown(dir, 0, gid); err != nil {
		return fmt.Errorf("setting the owner of %s: %w", dir, err)
	}
	return os.Chmod(dir, 0o750)
}

func listenerFiles(listeners []net.Listener) ([]*os.File, error) {
	files := make([]*os.File, 0, len(listeners))
	for _, l := range listeners {
		f, ok := l.(interface{ File() (*os.File, error) })
		if !ok {
			return nil, fmt.Errorf("%s cannot be handed to the front", l.Addr())
		}
		file, err := f.File()
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// superviseFront starts the front and starts it again whenever it exits, at
// most every thirty seconds when it keeps failing.
func superviseFront(uid, gid, listeners int, files []*os.File) {
	exe, err := os.Executable()
	if err != nil {
		slog.Error("the front cannot be started", slog.Any("err", err))
		os.Exit(1)
	}
	lifeline, write, err := os.Pipe()
	if err != nil {
		slog.Error("the front cannot be started", slog.Any("err", err))
		os.Exit(1)
	}
	frontLifeline = write

	backoff := time.Second
	for {
		cmd := exec.Command(exe, "front")
		cmd.Env = append(os.Environ(), frontListenersEnv+"="+strconv.Itoa(listeners))
		cmd.ExtraFiles = append([]*os.File{lifeline}, files...)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		cmd.Dir = "/"
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
			Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{uint32(gid)},
		}}
		started := time.Now()
		err := cmd.Run()
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		slog.Error("the front stopped; starting it again", slog.Any("err", err), slog.Duration("in", backoff))
		time.Sleep(backoff)
		backoff = min(backoff*2, 30*time.Second)
	}
}

// runFront is `jaildeck front`, which only the root side starts.
func runFront(cfg *config.Config) error {
	if os.Geteuid() == 0 {
		return errors.New("the front does not run as root; `jaildeck` started as root starts it (JD-056)")
	}
	n, err := strconv.Atoi(os.Getenv(frontListenersEnv))
	if err != nil || n < 1 {
		return errors.New("the front is started by Jail Deck itself, with its listeners (JD-056)")
	}
	lifeline := os.NewFile(3, "lifeline")
	go func() {
		_, _ = io.Copy(io.Discard, lifeline)
		slog.Info("the root side has stopped; so does the front")
		os.Exit(0)
	}()
	recheck, err := fileListener(4, "recheck")
	if err != nil {
		return err
	}
	public := make([]net.Listener, 0, n)
	for i := range n {
		l, err := fileListener(uintptr(5+i), "listener")
		if err != nil {
			return err
		}
		public = append(public, l)
	}

	apiSocket := filepath.Join(cfg.RunDir, apiSocketName)
	front, err := app.NewFront(cfg, func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", apiSocket)
	})
	if err != nil {
		return err
	}
	go func() {
		srv := &http.Server{Handler: front.RecheckRoutes(), ReadHeaderTimeout: 10 * time.Second}
		slog.Error("the front's recheck socket stopped", slog.Any("err", srv.Serve(recheck)))
		os.Exit(1)
	}()
	srv := newServer(net.JoinHostPort(cfg.Host, cfg.Port), front.Routes(web.Assets()))
	failed := make(chan error, len(public))
	for _, l := range public {
		go func(l net.Listener) { failed <- srv.Serve(l) }(l)
	}
	return <-failed
}

func fileListener(fd uintptr, name string) (net.Listener, error) {
	f := os.NewFile(fd, name)
	if f == nil {
		return nil, fmt.Errorf("the front was started without its %s", name)
	}
	defer f.Close()
	l, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("the front's %s: %w", name, err)
	}
	return l, nil
}

// splitUser is the account the front runs as, or nil to run as one
// process: when Jail Deck is not root, or JAILDECK_SPLIT=off.
func splitUser(cfg *config.Config) (*user.User, error) {
	if os.Geteuid() != 0 || cfg.OneProcess {
		return nil, nil
	}
	account, err := user.Lookup(cfg.User)
	if err != nil {
		return nil, fmt.Errorf("Jail Deck runs its front as the user %q (JD-056), which this host does not have (%v). "+
			"The package creates it; or set JAILDECK_SPLIT=off to run as one process, as root", cfg.User, err)
	}
	if account.Uid == "0" {
		return nil, fmt.Errorf("JAILDECK_USER=%s is root, which the front is there not to be (JD-056)", cfg.User)
	}
	return account, nil
}
