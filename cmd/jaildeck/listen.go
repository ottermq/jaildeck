package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"
)

// socketMode is fixed by JD-002: mode 0660 with a configurable group, so file
// permissions are a first access check on a local automation client. They are
// not the only one — RequireAuth still demands a token on this listener.
const socketMode = 0o660

// socketUmask leaves net.Listen creating the socket already at socketMode.
// Chmod after listening would work too, but a listening socket queues
// connections from the moment it exists, so between Listen and Chmod the
// default 0755 would be world-connectable. Closing that window is cheaper than
// reasoning about it.
const socketUmask = 0o777 &^ socketMode

// staleSocketDialTimeout bounds the liveness probe below. A unix socket answers
// or refuses immediately; this exists only so a pathological case cannot hang
// startup.
const staleSocketDialTimeout = 2 * time.Second

// listenUnix prepares path as a unix socket listener owned by group. An empty
// group leaves the socket's group as the process's own, which for a root
// service is wheel.
func listenUnix(path, group string) (net.Listener, error) {
	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}

	// Umask is process-global, so this is deliberately confined to the one call
	// that needs it and restored immediately. Nothing else in Jail Deck creates
	// files before the listeners are up.
	old := syscall.Umask(socketUmask)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", path, err)
	}

	if err := chownSocketGroup(path, group); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// clearStaleSocket removes a socket left behind by a process that did not shut
// down cleanly, and refuses to touch one that is still being served. Binding
// over an existing path fails with EADDRINUSE whether or not anything is
// listening, so the two cases have to be told apart before the bind, not after.
func clearStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		// Not ours to remove. A regular file or a directory at this path is a
		// configuration mistake, and deleting it would be a worse answer.
		return fmt.Errorf("%s exists and is not a socket", path)
	}

	conn, err := net.DialTimeout("unix", path, staleSocketDialTimeout)
	if err == nil {
		conn.Close()
		return fmt.Errorf("%s is already being served by another process", path)
	}
	// Refused or unreachable: nothing is listening, so the file is a leftover.
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing the stale socket %s: %w", path, err)
	}
	return nil
}

func chownSocketGroup(path, group string) error {
	if group == "" {
		return nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("looking up the socket group %q: %w", group, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("group %q has a non-numeric gid %q: %w", group, g.Gid, err)
	}
	// -1 leaves the owner alone; only the group is configurable (JD-002).
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("setting the group of %s to %q: %w", path, group, err)
	}
	return nil
}
