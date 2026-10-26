package vms

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/audit"
	"github.com/ottermq/jaildeck/internal/system"
)

// guestScript is vm-bhyve as a sequence: the state the guest "web" is listed
// in on each look, the last one repeating, and what each lifecycle command
// answers. Every command run and every audit entry is recorded.
type guestScript struct {
	states   []string
	looks    int
	startErr error
	stopErr  error
	offErr   error
	ran      []string
}

func (g *guestScript) VMList(context.Context) (Listing, error) {
	state := g.states[min(g.looks, len(g.states)-1)]
	g.looks++
	vm := VM{Name: "web", Datastore: "default", State: state}
	if state == StateLocked {
		vm.LockedBy = "orthanc"
	}
	return Listing{VMBhyve: VMBhyveReady, VMs: []VM{vm}}, nil
}

func (g *guestScript) VMInfo(context.Context, string) (Info, error) { return Info{}, nil }

func (g *guestScript) VMStart(_ context.Context, name string) (string, error) {
	g.ran = append(g.ran, "vm start "+name)
	return "vm start " + name, g.startErr
}

func (g *guestScript) VMStop(_ context.Context, name string) (string, error) {
	g.ran = append(g.ran, "vm stop "+name)
	return "vm stop " + name, g.stopErr
}

func (g *guestScript) VMPoweroff(_ context.Context, name string) (string, error) {
	g.ran = append(g.ran, "vm poweroff -f "+name)
	return "vm poweroff -f " + name, g.offErr
}

type auditRecorder struct{ entries []audit.Entry }

func (a *auditRecorder) Log(_ context.Context, e audit.Entry) error {
	a.entries = append(a.entries, e)
	return nil
}

func (a *auditRecorder) summary() []string {
	out := make([]string, 0, len(a.entries))
	for _, e := range a.entries {
		ok := "ok"
		if !e.Success {
			ok = "failed"
		}
		out = append(out, e.Operation+" "+e.Command+" "+ok)
	}
	return out
}

// lifecycle is a service over the script whose waits cost nothing and are
// counted, in seconds of the guest's time.
func lifecycle(g *guestScript) (*Service, *auditRecorder, *time.Duration) {
	rec := &auditRecorder{}
	s := NewService(g, rec)
	waited := new(time.Duration)
	s.wait = func(_ context.Context, d time.Duration) { *waited += d }
	return s, rec, waited
}

// runningFor is the look that decides, n more looks at a running guest, and
// then the state it ends in.
func runningFor(n int, then string) []string {
	states := []string{StateRunning} // the look that decides what to do
	for range n {
		states = append(states, StateRunning)
	}
	return append(states, then)
}

// --- start ----------------------------------------------------------------

func TestStartRunsVMStartAndWaitsUntilListedRunning(t *testing.T) {
	// Stopped when asked, still Stopped on the first look after vm start --
	// measured: vm-bhyve lists it Running a second later.
	g := &guestScript{states: []string{StateStopped, StateStopped, StateRunning}}
	s, rec, _ := lifecycle(g)
	result, err := s.Start(context.Background(), "web")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if result.State != StateRunning || result.Forced {
		t.Errorf("result = %+v, want running", result)
	}
	if want := []string{"vm-start vm start web ok"}; !reflect.DeepEqual(rec.summary(), want) {
		t.Errorf("audit = %v, want %v", rec.summary(), want)
	}
}

// Idempotent, as a jail's start is: nothing runs, so nothing is audited.
func TestStartingARunningGuestRunsNothing(t *testing.T) {
	for _, state := range []string{StateRunning, StateBootloader} {
		g := &guestScript{states: []string{state}}
		s, rec, _ := lifecycle(g)
		result, err := s.Start(context.Background(), "web")
		if err != nil || result.State != state || len(g.ran) != 0 || len(rec.entries) != 0 {
			t.Errorf("%s: result = %+v, err = %v, ran %v, audit %v", state, result, err, g.ran, rec.summary())
		}
	}
}

// A suspended guest is started: vm start resumes it from its saved state.
func TestStartResumesASuspendedGuest(t *testing.T) {
	g := &guestScript{states: []string{StateSuspended, StateRunning}}
	s, _, _ := lifecycle(g)
	if _, err := s.Start(context.Background(), "web"); err != nil || len(g.ran) != 1 {
		t.Errorf("err = %v, ran %v; want vm start", err, g.ran)
	}
}

// vm start answered, and the guest never came up: a failure, audited as one,
// that points at the log vm-bhyve keeps.
func TestStartThatNeverComesUpIsAFailure(t *testing.T) {
	g := &guestScript{states: []string{StateStopped}}
	s, rec, waited := lifecycle(g)
	_, err := s.Start(context.Background(), "web")
	if statusOf(t, err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "vm-bhyve.log") {
		t.Errorf("err = %v, want a 500 naming vm-bhyve.log", err)
	}
	if *waited < StartTimeout {
		t.Errorf("waited %s, want at least %s", *waited, StartTimeout)
	}
	if want := []string{"vm-start vm start web failed"}; !reflect.DeepEqual(rec.summary(), want) {
		t.Errorf("audit = %v, want %v", rec.summary(), want)
	}
}

// vm-bhyve's refusal, in its words, as the error.
func TestStartRefusedByVMBhyve(t *testing.T) {
	refusal := &system.CommandError{Command: "vm", Args: []string{"start", "web"},
		Result: system.CommandResult{ExitCode: 1},
		Err:    errors.New("exit status 1: incomplete virtual machine configuration")}
	g := &guestScript{states: []string{StateStopped}, startErr: refusal}
	s, rec, _ := lifecycle(g)
	_, err := s.Start(context.Background(), "web")
	if statusOf(t, err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "incomplete virtual machine configuration") {
		t.Errorf("err = %v", err)
	}
	if len(rec.entries) != 1 || rec.entries[0].ExitCode != 1 || rec.entries[0].Success {
		t.Errorf("audit = %+v, want one failed entry with exit 1", rec.entries)
	}
}

func TestStartTimingOutIs504(t *testing.T) {
	g := &guestScript{states: []string{StateStopped}, startErr: context.DeadlineExceeded}
	s, rec, _ := lifecycle(g)
	_, err := s.Start(context.Background(), "web")
	if statusOf(t, err) != http.StatusGatewayTimeout || !rec.entries[0].TimedOut {
		t.Errorf("err = %v, audit = %+v; want 504 and timed out", err, rec.entries)
	}
}

// --- stop -----------------------------------------------------------------

// The power button answered: no poweroff, not forced.
func TestStopThatTheGuestAnswers(t *testing.T) {
	g := &guestScript{states: runningFor(4, StateStopped)}
	s, rec, _ := lifecycle(g)
	result, err := s.Stop(context.Background(), "web")
	if err != nil || result.State != StateStopped || result.Forced {
		t.Fatalf("result = %+v, err = %v; want stopped, not forced", result, err)
	}
	if want := []string{"vm stop web"}; !reflect.DeepEqual(g.ran, want) {
		t.Errorf("ran %v, want %v", g.ran, want)
	}
	if want := []string{"vm-stop vm stop web ok"}; !reflect.DeepEqual(rec.summary(), want) {
		t.Errorf("audit = %v, want %v", rec.summary(), want)
	}
}

// JD-019's guest that ignores the power button: the deadline, then poweroff,
// and the answer says it was forced. Both steps are audited, the stop as the
// failure it was.
func TestStopFallsBackToPoweroffAtTheDeadline(t *testing.T) {
	looksUntilDeadline := int(StopTimeout / pollInterval)
	g := &guestScript{states: runningFor(looksUntilDeadline+1, StateStopped)}
	s, rec, waited := lifecycle(g)
	result, err := s.Stop(context.Background(), "web")
	if err != nil || result.State != StateStopped || !result.Forced {
		t.Fatalf("result = %+v, err = %v; want stopped and forced", result, err)
	}
	if want := []string{"vm stop web", "vm poweroff -f web"}; !reflect.DeepEqual(g.ran, want) {
		t.Errorf("ran %v, want %v", g.ran, want)
	}
	if want := []string{"vm-stop vm stop web failed", "vm-poweroff vm poweroff -f web ok"}; !reflect.DeepEqual(rec.summary(), want) {
		t.Errorf("audit = %v, want %v", rec.summary(), want)
	}
	if !strings.Contains(rec.entries[0].Error, "did not answer the ACPI shutdown within 1m0s") {
		t.Errorf("stop's audit error = %q", rec.entries[0].Error)
	}
	if *waited < StopTimeout {
		t.Errorf("gave the guest %s, want the whole %s", *waited, StopTimeout)
	}
}

// One look short of the deadline is not the deadline.
func TestStopDoesNotPowerOffAGuestThatStopsJustInTime(t *testing.T) {
	looksUntilDeadline := int(StopTimeout / pollInterval)
	g := &guestScript{states: runningFor(looksUntilDeadline, StateStopped)}
	s, _, _ := lifecycle(g)
	result, err := s.Stop(context.Background(), "web")
	if err != nil || result.Forced || len(g.ran) != 1 {
		t.Errorf("result = %+v, err = %v, ran %v; want a clean stop", result, err, g.ran)
	}
}

// Stopped between the deadline and the poweroff, which vm-bhyve then refuses
// -- "doesn't appear to be a running virtual machine". The guest is stopped,
// which was the request.
func TestStopThatRacesThePoweroffIsStillAStop(t *testing.T) {
	looksUntilDeadline := int(StopTimeout / pollInterval)
	g := &guestScript{
		states: runningFor(looksUntilDeadline, StateRunning),
		offErr: errors.New("exit status 1: jd doesn't appear to be a running virtual machine"),
	}
	g.states = append(g.states, StateStopped)
	s, rec, _ := lifecycle(g)
	result, err := s.Stop(context.Background(), "web")
	if err != nil || result.State != StateStopped {
		t.Errorf("result = %+v, err = %v; want stopped", result, err)
	}
	// The refused poweroff still ran, so it is still in the log, as refused.
	if want := []string{"vm-stop vm stop web failed", "vm-poweroff vm poweroff -f web failed"}; !reflect.DeepEqual(rec.summary(), want) {
		t.Errorf("audit = %v, want %v", rec.summary(), want)
	}
}

func TestStopThatEvenPoweroffCannotStopIsAFailure(t *testing.T) {
	g := &guestScript{states: []string{StateRunning}}
	s, rec, _ := lifecycle(g)
	_, err := s.Stop(context.Background(), "web")
	if statusOf(t, err) != http.StatusInternalServerError || !strings.Contains(err.Error(), "still listed as running") {
		t.Errorf("err = %v", err)
	}
	if len(rec.entries) != 2 || rec.entries[1].Success {
		t.Errorf("audit = %v, want the poweroff audited as failed", rec.summary())
	}
}

func TestStoppingAStoppedGuestRunsNothing(t *testing.T) {
	for _, state := range []string{StateStopped, StateSuspended} {
		g := &guestScript{states: []string{state}}
		s, rec, _ := lifecycle(g)
		result, err := s.Stop(context.Background(), "web")
		if err != nil || result.State != state || len(g.ran) != 0 || len(rec.entries) != 0 {
			t.Errorf("%s: result = %+v, err = %v, ran %v", state, result, err, g.ran)
		}
	}
}

// --- refused ----------------------------------------------------------------

// Locked by another host: refused, whatever was asked. In its boot loader:
// vm-bhyve's stop would ask on a terminal and quietly decline without one.
func TestLockedAndBootloaderAreRefusedBeforeAnythingRuns(t *testing.T) {
	type op func(*Service) (OperationResult, error)
	start := func(s *Service) (OperationResult, error) { return s.Start(context.Background(), "web") }
	stop := func(s *Service) (OperationResult, error) { return s.Stop(context.Background(), "web") }
	restart := func(s *Service) (OperationResult, error) { return s.Restart(context.Background(), "web") }
	for _, tt := range []struct {
		state string
		ops   map[string]op
		says  string
	}{
		{StateLocked, map[string]op{"start": start, "stop": stop, "restart": restart}, "locked by orthanc"},
		{StateBootloader, map[string]op{"stop": stop, "restart": restart}, "boot loader"},
	} {
		for name, call := range tt.ops {
			g := &guestScript{states: []string{tt.state}}
			s, _, _ := lifecycle(g)
			_, err := call(s)
			if statusOf(t, err) != http.StatusConflict || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("%s %s: err = %v, want a 409 saying %q", name, tt.state, err, tt.says)
			}
			if len(g.ran) != 0 {
				t.Errorf("%s %s: ran %v", name, tt.state, g.ran)
			}
		}
	}
}

func TestLifecycleRefusesWhatCannotBeAGuestName(t *testing.T) {
	g := &guestScript{states: []string{StateStopped}}
	s, _, _ := lifecycle(g)
	if _, err := s.Start(context.Background(), "-web"); statusOf(t, err) != http.StatusBadRequest {
		t.Errorf("err = %v, want 400", err)
	}
	if g.looks != 0 || len(g.ran) != 0 {
		t.Errorf("asked vm-bhyve %d times and ran %v before refusing", g.looks, g.ran)
	}
}

// --- restart --------------------------------------------------------------

func TestRestartStopsThenStarts(t *testing.T) {
	g := &guestScript{states: []string{StateRunning, StateStopped, StateRunning}}
	s, rec, _ := lifecycle(g)
	result, err := s.Restart(context.Background(), "web")
	if err != nil || result.State != StateRunning || result.Forced {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if want := []string{"vm stop web", "vm start web"}; !reflect.DeepEqual(g.ran, want) {
		t.Errorf("ran %v, want %v", g.ran, want)
	}
	if len(rec.entries) != 2 {
		t.Errorf("audit = %v", rec.summary())
	}
}

// A restart whose stop had to power off says so, though the guest is running
// again by the time it answers.
func TestRestartReportsAForcedStop(t *testing.T) {
	looksUntilDeadline := int(StopTimeout / pollInterval)
	states := runningFor(looksUntilDeadline+1, StateStopped)
	g := &guestScript{states: append(states, StateRunning)}
	s, _, _ := lifecycle(g)
	result, err := s.Restart(context.Background(), "web")
	if err != nil || result.State != StateRunning || !result.Forced {
		t.Errorf("result = %+v, err = %v; want running and forced", result, err)
	}
}

func TestRestartOfAStoppedGuestOnlyStartsIt(t *testing.T) {
	g := &guestScript{states: []string{StateStopped, StateRunning}}
	s, _, _ := lifecycle(g)
	if _, err := s.Restart(context.Background(), "web"); err != nil || !reflect.DeepEqual(g.ran, []string{"vm start web"}) {
		t.Errorf("err = %v, ran %v", err, g.ran)
	}
}

// A failed stop is not followed by a start.
func TestRestartWhoseStopFailsDoesNotStart(t *testing.T) {
	g := &guestScript{states: []string{StateRunning}, stopErr: errors.New("exit status 1")}
	s, _, _ := lifecycle(g)
	if _, err := s.Restart(context.Background(), "web"); err == nil || len(g.ran) != 1 {
		t.Errorf("err = %v, ran %v; want the stop alone, failed", err, g.ran)
	}
}

// The client going away does not abort a lifecycle step half-way.
func TestLifecycleIgnoresTheClientGoingAway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Still stopped on the first look after vm start, so it has to wait.
	g := &guestScript{states: []string{StateStopped, StateStopped, StateRunning}}
	s, _, _ := lifecycle(g)
	var seen context.Context
	s.wait = func(c context.Context, _ time.Duration) { seen = c }
	if _, err := s.Start(ctx, "web"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if seen == nil {
		t.Fatal("never waited; the check would prove nothing")
	}
	if seen.Err() != nil {
		t.Error("the wait saw the client's cancellation")
	}
}
