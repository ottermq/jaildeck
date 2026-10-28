// ptyprobe measures a shell in a jail on a PTY, started the way Jail Deck's
// console starts it: jexec -l <jail> /bin/sh -l, through creack/pty.
// Usage: ptyprobe <jail> <phase>
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type session struct {
	cmd  *exec.Cmd
	tty  *os.File
	mu   sync.Mutex
	out  []byte
	done chan error // the read loop's ending error
}

// nonblocking swaps the PTY master for a non-blocking duplicate of it, which
// Go's poller manages, so that Close interrupts a blocked Read. The original
// *os.File is closed only after the duplicate exists; its Fd() would have put
// the descriptor back into blocking mode.
func nonblocking(tty *os.File) *os.File {
	raw, err := tty.SyscallConn()
	if err != nil {
		fmt.Println("SyscallConn:", err)
		os.Exit(1)
	}
	var dup int
	var dupErr error
	_ = raw.Control(func(fd uintptr) { dup, dupErr = unix.Dup(int(fd)) })
	if dupErr != nil {
		fmt.Println("dup:", dupErr)
		os.Exit(1)
	}
	unix.CloseOnExec(dup)
	if err := unix.SetNonblock(dup, true); err != nil {
		fmt.Println("SetNonblock:", err)
		os.Exit(1)
	}
	tty.Close()
	return os.NewFile(uintptr(dup), "ptmx")
}

var makeNonblocking bool

func start(jail string) *session {
	cmd := exec.Command("jexec", "-l", jail, "/bin/sh", "-l")
	// What the console would pass, and one variable that must not get in.
	cmd.Env = []string{"TERM=xterm-256color", "JD_PROBE_LEAK=host-secret", "PATH=/sbin:/bin:/usr/sbin:/usr/bin"}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		fmt.Println("start:", err)
		os.Exit(1)
	}
	if makeNonblocking {
		tty = nonblocking(tty)
	}
	s := &session{cmd: cmd, tty: tty, done: make(chan error, 1)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := tty.Read(buf)
			s.mu.Lock()
			s.out = append(s.out, buf[:n]...)
			s.mu.Unlock()
			if err != nil {
				s.done <- err
				return
			}
		}
	}()
	return s
}

func (s *session) typed(line string, wait time.Duration) string {
	s.mu.Lock()
	from := len(s.out)
	s.mu.Unlock()
	if _, err := s.tty.Write([]byte(line + "\r")); err != nil {
		return "write: " + err.Error()
	}
	time.Sleep(wait)
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.out[from:])
}

func (s *session) ending(limit time.Duration) string {
	t0 := time.Now()
	select {
	case err := <-s.done:
		w := s.cmd.Wait()
		return fmt.Sprintf("read ended after %.1fs with %q; jexec: %v", time.Since(t0).Seconds(), err.Error(), w)
	case <-time.After(limit):
		return fmt.Sprintf("read still open after %s", limit)
	}
}

func main() {
	jail, phase := os.Args[1], os.Args[2]
	if phase == "hangupnb" || phase == "basicnb" {
		makeNonblocking = true
		phase = strings.TrimSuffix(phase, "nb")
	}
	s := start(jail)
	time.Sleep(time.Second)
	switch phase {
	case "basic":
		fmt.Printf("env:\n%s\n", s.typed("env | sort", time.Second))
		fmt.Printf("size at start: %q\n", s.typed("stty size", time.Second))
		_ = pty.Setsize(s.tty, &pty.Winsize{Rows: 40, Cols: 120})
		fmt.Printf("size after Setsize 40x120: %q\n", s.typed("stty size", time.Second))
		fmt.Printf("tty: %q\n", s.typed("tty", time.Second))
		fmt.Printf("exit: %q\n", s.typed("exit", time.Second))
		fmt.Println(s.ending(5 * time.Second))
	case "stop":
		fmt.Printf("ps: %q\n", s.typed("echo $$", time.Second))
		fmt.Println("READY")
		fmt.Println(s.ending(60 * time.Second))
	case "hangup":
		fmt.Printf("shell pid: %q\n", s.typed("echo $$", time.Second))
		s.typed("sleep 600", 500*time.Millisecond)
		// Our side goes away while a command runs in the shell: the master
		// closed, and jexec left to whatever that does to it.
		fmt.Println("closing the master")
		_ = s.tty.Close()
		fmt.Println(s.ending(10 * time.Second))
		time.Sleep(time.Second)
		if s.cmd.ProcessState == nil {
			fmt.Println("jexec after hangup:", s.cmd.Wait())
		}
	}
}
