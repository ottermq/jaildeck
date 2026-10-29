package console_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/console"
	"github.com/ottermq/jaildeck/internal/system/fake"
)

// jailStates is which jails run: nginx does, postgres does not, and anything
// else is not there.
type jailStates map[string]bool

func (j jailStates) Running(_ context.Context, name string) (bool, error) {
	running, ok := j[name]
	if !ok {
		return false, common.NewAppError(http.StatusNotFound, "jail not found")
	}
	return running, nil
}

// guests maps a guest to its console device; "" is a guest that is not
// running, and a name not here is no guest at all.
type guests struct {
	mu     sync.Mutex
	states map[string]string
}

func (g *guests) Console(_ context.Context, name string) (string, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	device, ok := g.states[name]
	if !ok {
		return "", false, common.NewAppError(http.StatusNotFound, "vm-bhyve has no virtual machine named "+name)
	}
	return device, device != "", nil
}

func (g *guests) stop(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.states[name] = ""
}

type recorder struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recorder) Log(_ context.Context, e audit.Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
	return nil
}

func (r *recorder) all() []audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]audit.Entry(nil), r.entries...)
}

type rig struct {
	shells  *fake.Shells
	guests  *guests
	service *console.Service
	handler *console.Handler
	audit   *recorder
	expired atomic.Bool
	url     string
}

func newRig(t *testing.T, idle time.Duration, origins ...string) *rig {
	t.Helper()
	rg := &rig{audit: &recorder{}, guests: &guests{states: map[string]string{
		"web": "/dev/nmdm-web.1B", "db": "", "odd": "/dev/ttyu0"}}}
	rg.shells = fake.NewShells()
	rg.service = console.NewService(rg.shells, jailStates{"nginx": true, "postgres": false}, rg.guests, rg.audit)
	rg.handler = console.NewHandler(rg.service,
		func(*http.Request) error {
			if rg.expired.Load() {
				return errors.New("session expired")
			}
			return nil
		},
		func(*http.Request) string { return "session" }, idle, origins)
	console.SetHeartbeat(rg.handler, 20*time.Millisecond)
	console.SetGuestWatch(rg.service, 20*time.Millisecond)
	r := chi.NewRouter()
	r.Get("/jails/{name}/console", rg.handler.Jail)
	r.Get("/vms/{name}/console", rg.handler.VM)
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	rg.url = "ws" + strings.TrimPrefix(server.URL, "http")
	return rg
}

// terminal reads frames until the output holds want, or a closed message
// comes, and answers the output and the closing reason.
type terminal struct {
	t    *testing.T
	conn *websocket.Conn
	out  strings.Builder
}

func dial(t *testing.T, url string, header http.Header) (*terminal, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		return nil, resp, err
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return &terminal{t: t, conn: conn}, resp, nil
}

func (tm *terminal) until(want string) (closed string) {
	tm.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for !strings.Contains(tm.out.String(), want) {
		kind, data, err := tm.conn.Read(ctx)
		if err != nil {
			tm.t.Fatalf("waiting for %q: %v; have %q", want, err, tm.out.String())
		}
		if kind == websocket.MessageText {
			var message struct{ Type, Reason string }
			_ = json.Unmarshal(data, &message)
			if message.Type == "closed" {
				return message.Reason
			}
			continue
		}
		tm.out.Write(data)
	}
	return ""
}

// closing reads until the server's closed message, and answers its reason.
func (tm *terminal) closing() string {
	tm.t.Helper()
	if reason := tm.until("\x00never\x00"); reason != "" {
		return reason
	}
	return ""
}

func (tm *terminal) typed(s string) {
	tm.t.Helper()
	if err := tm.conn.Write(context.Background(), websocket.MessageBinary, []byte(s)); err != nil {
		tm.t.Fatal(err)
	}
}

func (tm *terminal) resize(cols, rows int) {
	tm.t.Helper()
	b, _ := json.Marshal(map[string]any{"type": "resize", "cols": cols, "rows": rows})
	if err := tm.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		tm.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Errorf("never: %s", what)
}

func TestAShellAnswersResizesAndExits(t *testing.T) {
	rg := newRig(t, time.Hour)
	tm, _, err := dial(t, rg.url+"/jails/nginx/console?cols=100&rows=30", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("root@nginx:/ # ")
	tm.typed("stty size\r")
	tm.until("30 100")
	tm.resize(120, 40)
	tm.typed("hostname\r")
	tm.until("\r\nnginx")
	tm.typed("stty size\r")
	tm.until("40 120")
	tm.typed("exit\r")
	if reason := tm.closing(); reason != console.ReasonShellExited {
		t.Errorf("closed with %q, want %q", reason, console.ReasonShellExited)
	}

	eventually(t, "the session is gone and its close audited", func() bool { return rg.service.Open() == 0 && len(rg.audit.all()) == 2 })
	entries := rg.audit.all()
	open, closed := entries[0], entries[1]
	if open.Operation != console.OperationOpen || open.Target != "jail/nginx" || open.Principal != "session" ||
		open.Command != "jexec -l nginx /bin/sh -l" || !open.Success {
		t.Errorf("open entry = %+v", open)
	}
	if closed.Operation != console.OperationClose || closed.Reason != console.ReasonShellExited || closed.Principal != "session" ||
		closed.DurationMS <= 0 {
		t.Errorf("close entry = %+v", closed)
	}
	for _, e := range entries {
		if strings.Contains(e.Command+e.Error, "hostname") || strings.Contains(e.Command+e.Error, "stty") {
			t.Errorf("something typed reached the audit log: %+v", e)
		}
	}
}

func TestRefusalsComeBeforeTheUpgradeAndOpenNothing(t *testing.T) {
	rg := newRig(t, time.Hour)
	for _, tc := range []struct {
		path   string
		header http.Header
		code   int
	}{
		{"/jails/postgres/console", nil, http.StatusConflict},
		{"/jails/nosuch/console", nil, http.StatusNotFound},
		{"/jails/nginx/console?cols=0", nil, http.StatusBadRequest},
		{"/jails/nginx/console?rows=5000", nil, http.StatusBadRequest},
		{"/jails/nginx/console", http.Header{"Origin": {"https://evil.example"}}, http.StatusForbidden},
	} {
		_, resp, err := dial(t, rg.url+tc.path, tc.header)
		if err == nil || resp == nil || resp.StatusCode != tc.code {
			code := 0
			if resp != nil {
				code = resp.StatusCode
			}
			t.Errorf("%s %v: %d, %v; want %d", tc.path, tc.header, code, err, tc.code)
		}
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Errorf("%d audit entries; a refused console opens nothing", n)
	}

	// Not a WebSocket at all.
	resp, err := http.Get("http" + strings.TrimPrefix(rg.url, "ws") + "/jails/nginx/console")
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a plain GET: %v, %v; want 400", resp, err)
	}
}

func TestOriginsThisHostAndConfigured(t *testing.T) {
	rg := newRig(t, time.Hour, "*.example.org")
	host := strings.TrimPrefix(rg.url, "ws://")
	for _, origin := range []string{"http://" + host, "https://deck.example.org"} {
		tm, resp, err := dial(t, rg.url+"/jails/nginx/console", http.Header{"Origin": {origin}})
		if err != nil {
			t.Errorf("Origin %s refused: %v %v", origin, resp, err)
			continue
		}
		tm.until("root@nginx")
	}
}

func TestNothingTypedEndsTheSessionButOutputDoesNot(t *testing.T) {
	rg := newRig(t, 150*time.Millisecond)
	tm, _, err := dial(t, rg.url+"/jails/nginx/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("# ")
	// Typing keeps it open past the idle time: 300 ms of keys, twice the
	// idle time, and the shell still answers.
	for i := 0; i < 6; i++ {
		time.Sleep(50 * time.Millisecond)
		tm.typed("\x7f")
	}
	tm.typed("hostname\r")
	tm.until("\r\nnginx")
	if reason := tm.closing(); reason != console.ReasonIdle {
		t.Errorf("closed with %q, want %q", reason, console.ReasonIdle)
	}
}

func TestALoginThatExpiresEndsTheSession(t *testing.T) {
	rg := newRig(t, time.Hour)
	tm, _, err := dial(t, rg.url+"/jails/nginx/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("# ")
	rg.expired.Store(true)
	if reason := tm.closing(); reason != console.ReasonSessionExpired {
		t.Errorf("closed with %q, want %q", reason, console.ReasonSessionExpired)
	}
}

func TestAJailStoppingHangsItsConsolesUp(t *testing.T) {
	rg := newRig(t, time.Hour)
	var terms []*terminal
	for i := 0; i < 2; i++ {
		tm, _, err := dial(t, rg.url+"/jails/nginx/console", nil)
		if err != nil {
			t.Fatal(err)
		}
		tm.until("# ")
		terms = append(terms, tm)
	}
	rg.service.HangUpJail("nginx")
	for _, tm := range terms {
		if reason := tm.closing(); reason != console.ReasonJailStopped {
			t.Errorf("closed with %q, want %q", reason, console.ReasonJailStopped)
		}
	}
	eventually(t, "both sessions finished", func() bool { return rg.service.Open() == 0 })
	closes := 0
	for _, e := range rg.audit.all() {
		if e.Operation == console.OperationClose {
			closes++
			if e.Reason != console.ReasonJailStopped {
				t.Errorf("close entry reason %q, want %q: the hang-up came first", e.Reason, console.ReasonJailStopped)
			}
		}
	}
	if closes != 2 {
		t.Errorf("%d close entries, want 2", closes)
	}
}

// The server's ReadTimeout set a deadline on the connection when the request
// began, and a hijacked connection keeps it: a console must outlive it.
func TestAConsoleOutlivesTheServersReadTimeout(t *testing.T) {
	rg := newRig(t, time.Hour)
	r := chi.NewRouter()
	r.Get("/jails/{name}/console", rg.handler.Jail)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: r, ReadHeaderTimeout: time.Second, ReadTimeout: 200 * time.Millisecond}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	tm, _, err := dial(t, "ws://"+listener.Addr().String()+"/jails/nginx/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("# ")
	time.Sleep(600 * time.Millisecond)
	tm.typed("hostname\r")
	tm.until("\r\nnginx")
}

// The first reason a session ended for is the one recorded: a jail that stops
// also makes the shell's read fail, and the bridge may report that instead.
func TestTheFirstReasonIsTheOneAudited(t *testing.T) {
	rg := newRig(t, time.Hour)
	session, err := rg.service.OpenJail(context.Background(), "nginx", "token:otter", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	rg.service.HangUpJail("nginx")
	rg.service.Finish(context.Background(), session, console.ReasonShellExited)
	entries := rg.audit.all()
	if last := entries[len(entries)-1]; last.Reason != console.ReasonJailStopped || last.Principal != "token:otter" {
		t.Errorf("close entry = %+v, want reason %q", last, console.ReasonJailStopped)
	}
}

// --- a guest's serial console --------------------------------------------------

func TestAGuestsConsoleIsItsLoginPromptAndOnlyOneAtATime(t *testing.T) {
	rg := newRig(t, time.Hour)
	tm, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("web login: ")
	tm.typed("\r")
	tm.until("\r\nweb login: ")

	if _, resp, err := dial(t, rg.url+"/vms/web/console", nil); err == nil || resp.StatusCode != http.StatusConflict {
		t.Errorf("a second console on web: %v %v, want 409", resp, err)
	}

	// Closing the first frees the guest's console for another.
	_ = tm.conn.Close(websocket.StatusNormalClosure, "")
	eventually(t, "the first session finished", func() bool { return rg.service.Open() == 0 })
	again, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatalf("after the first closed: %v", err)
	}
	again.until("web login: ")

	open := rg.audit.all()[0]
	if open.Operation != console.OperationOpen || open.Target != "vm/web" || open.Command != "open /dev/nmdm-web.1B" {
		t.Errorf("open entry = %+v", open)
	}
}

// A powered-off guest gives the device's reader nothing at all (measured), so
// the session looks at the guest and ends itself.
func TestAGuestThatStopsEndsItsConsole(t *testing.T) {
	rg := newRig(t, time.Hour)
	tm, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("login: ")
	rg.guests.stop("web")
	if reason := tm.closing(); reason != console.ReasonGuestStopped {
		t.Errorf("closed with %q, want %q", reason, console.ReasonGuestStopped)
	}
}

func TestAGuestsConsoleIsRefusedBeforeTheUpgrade(t *testing.T) {
	rg := newRig(t, time.Hour)
	for path, code := range map[string]int{
		"/vms/db/console":     http.StatusConflict,
		"/vms/nosuch/console": http.StatusNotFound,
		// vm info named something that is not this guest's nmdm device.
		"/vms/odd/console": http.StatusInternalServerError,
	} {
		if _, resp, err := dial(t, rg.url+path, nil); err == nil || resp == nil || resp.StatusCode != code {
			t.Errorf("%s: %v %v, want %d", path, resp, err, code)
		}
	}
	if _, resp, err := dial(t, rg.url+"/vms/web/console", http.Header{"Origin": {"https://evil.example"}}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("another origin: %v %v, want 403", resp, err)
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Errorf("%d audit entries; a refused console opens nothing", n)
	}
	// None of the refusals took web's one console.
	tm, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatalf("web after the refusals: %v", err)
	}
	tm.until("login: ")
}

// A console device that ends of itself ends the session as console-closed,
// not as a shell exiting. The fake's line ends on "exit"; a real device ends
// when its far side goes.
func TestAConsoleDeviceThatEndsSaysSo(t *testing.T) {
	rg := newRig(t, time.Hour)
	tm, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("login: ")
	tm.typed("exit\r")
	if reason := tm.closing(); reason != console.ReasonConsoleClosed {
		t.Errorf("closed with %q, want %q", reason, console.ReasonConsoleClosed)
	}
}

// failingConsoles fails to open a guest's console once.
type failingConsoles struct {
	console.Shells
	failed atomic.Bool
}

func (f *failingConsoles) VMConsole(ctx context.Context, device string) (console.Terminal, error) {
	if f.failed.CompareAndSwap(false, true) {
		return nil, errors.New("open /dev/nmdm-web.1B: device busy")
	}
	return f.Shells.VMConsole(ctx, device)
}

// A console that failed to open gives the guest's one console back.
func TestAFailedOpenFreesTheGuestsConsole(t *testing.T) {
	rec := &recorder{}
	g := &guests{states: map[string]string{"web": "/dev/nmdm-web.1B"}}
	service := console.NewService(&failingConsoles{Shells: fake.NewShells()}, jailStates{}, g, rec)
	if _, err := service.OpenVM(context.Background(), "web", "session"); err == nil {
		t.Fatal("the first open succeeded")
	}
	session, err := service.OpenVM(context.Background(), "web", "session")
	if err != nil {
		t.Fatalf("after a failed open: %v", err)
	}
	service.Finish(context.Background(), session, console.ReasonClient)
	if first := rec.all()[0]; first.Success || !strings.Contains(first.Error, "device busy") {
		t.Errorf("the failed open's entry = %+v", first)
	}
}

// Another reader on the device -- a vm console left open in a shell, as in
// #153 -- would get half of every line, and Jail Deck the other half. So the
// console is refused, naming it -- in a closed message the page can read --
// and nothing is audited as opened.
func TestAConsoleAnotherReaderHasIsRefused(t *testing.T) {
	rg := newRig(t, time.Hour)
	rg.shells.Hold("/dev/nmdm-web.1B", console.Holder{Command: "cu", PID: 38812}, console.Holder{Command: "cu", PID: 38813})
	// Accepted and closed at once, saying why: a browser cannot read a
	// refused handshake.
	refused, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := refused.conn.Read(ctx)
	var closed struct{ Type, Reason, Detail string }
	if err != nil || json.Unmarshal(data, &closed) != nil || closed.Reason != console.ReasonConsoleShared ||
		!strings.Contains(closed.Detail, "cu (pid 38812), cu (pid 38813)") || !strings.Contains(closed.Detail, "~.") {
		t.Fatalf("closed = %+v, %v", closed, err)
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Errorf("%d audit entries for a refused console", n)
	}
	// Closed in its own terminal, the console opens.
	rg.shells.Hold("/dev/nmdm-web.1B")
	tm, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatalf("after the other reader went: %v", err)
	}
	tm.until("login: ")
}

// A reader that turns up during a session ends it, with its own reason.
func TestAReaderThatTurnsUpEndsTheSession(t *testing.T) {
	rg := newRig(t, time.Hour)
	tm, _, err := dial(t, rg.url+"/vms/web/console", nil)
	if err != nil {
		t.Fatal(err)
	}
	tm.until("login: ")
	rg.shells.Hold("/dev/nmdm-web.1B", console.Holder{Command: "cu", PID: 4242})
	if reason := tm.closing(); reason != console.ReasonConsoleShared {
		t.Errorf("closed with %q, want %q", reason, console.ReasonConsoleShared)
	}
}
