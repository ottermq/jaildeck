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
	if err := app.RequireCredential(cfg); err != nil {
		return err
	}
	if err := shareStateDir(cfg.StateDir, uid); err != nil {
		return err
	}
	if err := prepareRunDir(cfg.RunDir); err != nil {
		return err
	}
	// The front's uid is the boundary, never a group: a host's jaildeck
	// group may already hold the clients of JAILDECK_SOCKET, and any of them
	// could then name any principal on the root side's socket.
	apiListener, err := listenPrivate(filepath.Join(cfg.RunDir, apiSocketName), uid)
	if err != nil {
		return err
	}
	apiListener = onlyFrom(apiListener, uid)
	recheckPath := filepath.Join(cfg.RunDir, recheckSocketName)
	recheckListener, err := listenPrivate(recheckPath, 0)
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

// shareStateDir makes the state file the front's to read: the file owned
// by the front's uid, 0600, in a directory of root's, 0711, that the front
// can pass through but nobody can list. Every start does it, which is also
// the upgrade from a Jail Deck that ran as one process. `jaildeck passwd`
// and `token` keep the owner when they replace the file. A directory holding
// anything but the state file is refused: it would not be Jail Deck's alone.
func shareStateDir(dir string, uid int) error {
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
		uid  int
		mode os.FileMode
	}{{dir, 0, 0o711}, {filepath.Join(dir, stateFileName), uid, 0o600}} {
		if err := os.Lchown(p.path, p.uid, 0); err != nil {
			return fmt.Errorf("sharing %s with the front: %w", p.path, err)
		}
		if err := os.Chmod(p.path, p.mode); err != nil {
			return fmt.Errorf("sharing %s with the front: %w", p.path, err)
		}
	}
	return nil
}

// prepareRunDir makes the directory the sockets live in: root's, 0755. The
// sockets in it are each 0600 and owned by the one uid that may use them.
func prepareRunDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if err := os.Chown(dir, 0, 0); err != nil {
		return fmt.Errorf("setting the owner of %s: %w", dir, err)
	}
	return os.Chmod(dir, 0o755)
}

// privateUmask creates a socket at 0600, so that nobody but root can connect
// in the moment before it is given to its uid.
const privateUmask = 0o177

// listenPrivate listens on a socket only uid (and root) can connect to.
func listenPrivate(path string, uid int) (net.Listener, error) {
	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}
	old := syscall.Umask(privateUmask)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}
	if err := os.Chown(path, uid, 0); err != nil {
		l.Close()
		return nil, fmt.Errorf("giving %s to uid %d: %w", path, uid, err)
	}
	return l, nil
}

// onlyFrom drops a connection from anyone but uid or root, by the kernel's
// account of who connected. The socket's mode already says so; this holds
// even if the mode is ever wrong.
func onlyFrom(l net.Listener, uid int) net.Listener {
	return &peerListener{Listener: l, uid: uid}
}

type peerListener struct {
	net.Listener
	uid int
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			c.Close()
			continue
		}
		peer, err := peerUID(uc)
		if err == nil && (peer == l.uid || peer == 0) {
			return c, nil
		}
		slog.Warn("refused a connection to the root side's socket", "uid", peer, slog.Any("err", err))
		c.Close()
	}
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
