// conprobe measures a bhyve guest's nmdm console from the host's end, with
// the same calls Jail Deck would make. Usage: conprobe <device> <phase>
package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func flags(fd int) string {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return "termios: " + err.Error()
	}
	on := func(v, bit uint32, name string) string {
		if v&bit != 0 {
			return name
		}
		return "-" + name
	}
	return strings.Join([]string{
		on(t.Lflag, unix.ECHO, "echo"), on(t.Lflag, unix.ICANON, "icanon"), on(t.Lflag, unix.ISIG, "isig"),
		on(t.Iflag, unix.ICRNL, "icrnl"), on(t.Iflag, unix.IXON, "ixon"), on(t.Oflag, unix.OPOST, "opost"),
	}, " ")
}

// drain reads for d and reports what came back.
func drain(f *os.File, d time.Duration) string {
	_ = f.SetReadDeadline(time.Now().Add(d))
	var all []byte
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		all = append(all, buf[:n]...)
		if err != nil {
			if os.IsTimeout(err) {
				return fmt.Sprintf("%d bytes: %q", len(all), tail(all))
			}
			return fmt.Sprintf("%d bytes then %v: %q", len(all), err, tail(all))
		}
		if n == 0 {
			return fmt.Sprintf("%d bytes then EOF (n=0): %q", len(all), tail(all))
		}
	}
}

// write writes with a deadline, so that a write the tty holds back is
// reported rather than hanging the probe.
func write(f *os.File, b string) string {
	_ = f.SetWriteDeadline(time.Now().Add(3 * time.Second))
	n, err := f.Write([]byte(b))
	return fmt.Sprintf("wrote %d of %d, err %v", n, len(b), err)
}

// stops counts the flow-control bytes in what came back: XOFF (^S) and XON (^Q).
func stops(s string) string {
	return fmt.Sprintf("XOFF %d, XON %d", strings.Count(s, "\x13"), strings.Count(s, "\x11"))
}

func tail(b []byte) []byte {
	if len(b) > 160 {
		return b[len(b)-160:]
	}
	return b
}

func open(dev string) (*os.File, error) {
	// O_NONBLOCK so that deadlines work: Go's poller only manages
	// nonblocking descriptors.
	fd, err := unix.Open(dev, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), dev), nil
}

func main() {
	dev, phase := os.Args[1], os.Args[2]
	f, err := open(dev)
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	fd := int(f.Fd())
	switch phase {
	case "modes":
		fmt.Println("default termios:", flags(fd))
		got := drain(f, 3*time.Second)
		fmt.Println("idle, default:", got, stops(got))
		fmt.Println("CR, default:", write(f, "\r"))
		got = drain(f, 3*time.Second)
		fmt.Println("  ->", got, stops(got))
		fmt.Println("'ver' CR, default:", write(f, "ver\r"))
		got = drain(f, 3*time.Second)
		fmt.Println("  ->", got, stops(got))
		if _, err := term.MakeRaw(fd); err != nil {
			fmt.Println("MakeRaw:", err)
		}
		fmt.Println("raw termios:", flags(fd))
		fmt.Println("'ver' CR, raw:", write(f, "ver\r"))
		got = drain(f, 3*time.Second)
		fmt.Println("  ->", got, stops(got))
		fmt.Println("ESC, raw:", write(f, "\x1b"))
		got = drain(f, 3*time.Second)
		fmt.Println("  ->", got, stops(got))
	case "duplex":
		// A reader drains the whole time, as a bridge would; meanwhile,
		// writes in the default mode and then in raw mode.
		var total int64
		done := make(chan struct{})
		go func() {
			buf := make([]byte, 4096)
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = f.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, _ := f.Read(buf)
				atomic.AddInt64(&total, int64(n))
			}
		}()
		time.Sleep(2 * time.Second)
		fmt.Println("default termios:", flags(fd))
		for i := 0; i < 5; i++ {
			fmt.Printf("default write %d: %s (read so far %d)\n", i, write(f, "\x1b"), atomic.LoadInt64(&total))
			time.Sleep(time.Second)
		}
		if _, err := term.MakeRaw(fd); err != nil {
			fmt.Println("MakeRaw:", err)
		}
		fmt.Println("raw termios:", flags(fd))
		for i := 0; i < 5; i++ {
			fmt.Printf("raw write %d: %s (read so far %d)\n", i, write(f, "\x1b"), atomic.LoadInt64(&total))
			time.Sleep(time.Second)
		}
		close(done)
		time.Sleep(time.Second)
	case "rawduplex", "cookedduplex":
		// A fresh open, raw at once or left in the default mode; a reader
		// drains the whole time and keeps everything; three returns are typed.
		if phase == "rawduplex" {
			if _, err := term.MakeRaw(fd); err != nil {
				fmt.Println("MakeRaw:", err)
			}
		}
		fmt.Println("termios:", flags(fd))
		var mu sync.Mutex
		var got []byte
		done := make(chan struct{})
		go func() {
			buf := make([]byte, 4096)
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = f.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, _ := f.Read(buf)
				mu.Lock()
				got = append(got, buf[:n]...)
				mu.Unlock()
			}
		}()
		for i := 0; i < 3; i++ {
			fmt.Printf("return %d: %s\n", i, write(f, "\r"))
			time.Sleep(3 * time.Second)
		}
		close(done)
		time.Sleep(time.Second)
		mu.Lock()
		out := got
		if len(out) > 700 {
			out = out[len(out)-700:]
		}
		fmt.Printf("read %d bytes, ending: %q\n", len(got), out)
		mu.Unlock()
	case "watch":
		// Raw from the start; everything for 100 seconds, a return every 20.
		_, _ = term.MakeRaw(fd)
		var all []byte
		buf := make([]byte, 4096)
		end := time.Now().Add(100 * time.Second)
		next := time.Now().Add(20 * time.Second)
		for time.Now().Before(end) {
			if time.Now().After(next) {
				fmt.Printf("[%s at %d bytes]\n", write(f, "\r"), len(all))
				next = next.Add(20 * time.Second)
			}
			_ = f.SetReadDeadline(time.Now().Add(time.Second))
			n, _ := f.Read(buf)
			all = append(all, buf[:n]...)
		}
		os.Stdout.Write(all)
	case "excl":
		err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0)
		fmt.Println("TIOCEXCL:", err)
		g, err := open(dev)
		fmt.Println("second open by root while exclusive:", err == nil, err)
		if g != nil {
			g.Close()
		}
		// Hold it so the script can try cu and vm console meanwhile.
		time.Sleep(8 * time.Second)
	case "stop":
		_, _ = term.MakeRaw(fd)
		fmt.Println("reading while the guest is stopped (60s at most):")
		start := time.Now()
		res := drain(f, 60*time.Second)
		fmt.Printf("after %.1fs: %s\n", time.Since(start).Seconds(), res)
		fmt.Println("write after:", write(f, "x"))
	case "hold":
		// Stays open across whatever the script does to the guest, then reads.
		_, _ = term.MakeRaw(fd)
		time.Sleep(25 * time.Second)
		fmt.Println("after the guest restarted:", write(f, "\r"), "->", drain(f, 4*time.Second))
	}
	f.Close()
}
