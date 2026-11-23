package jails

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/common"
)

// removalAfter says the jail is gone from the nth question on; zero never.
type removalAfter struct {
	n     int
	asked *int
	err   error
}

func (r removalAfter) Gone(context.Context, string) (bool, error) {
	*r.asked++
	return r.n > 0 && *r.asked >= r.n, r.err
}

func serviceWithRemoval(r JailRemoval) *JailService {
	s := NewJailService(Dependencies{})
	s.SetRemoval(r)
	s.goneWait, s.gonePoll = 50*time.Millisecond, time.Millisecond
	return s
}

func TestWaitGoneWaitsForADyingJail(t *testing.T) {
	asked := 0
	if err := serviceWithRemoval(removalAfter{n: 3, asked: &asked}).waitGone(context.Background(), "db"); err != nil {
		t.Fatalf("waitGone() error = %v", err)
	}
	if asked != 3 {
		t.Errorf("asked %d times, want 3", asked)
	}
}

func TestWaitGoneRefusesAJailThatStaysDying(t *testing.T) {
	asked := 0
	err := serviceWithRemoval(removalAfter{asked: &asked}).waitGone(context.Background(), "db")
	var appErr *common.AppError
	if !errors.As(err, &appErr) || appErr.Code != http.StatusConflict {
		t.Fatalf("waitGone() error = %v, want a 409", err)
	}
}

func TestWaitGoneReportsAFailedQuestion(t *testing.T) {
	asked := 0
	if err := serviceWithRemoval(removalAfter{asked: &asked, err: errors.New("jls: boom")}).
		waitGone(context.Background(), "db"); err == nil {
		t.Fatal("waitGone() returned no error when jls failed")
	}
}

func TestWaitGoneWithoutAPortDoesNotWait(t *testing.T) {
	if err := NewJailService(Dependencies{}).waitGone(context.Background(), "db"); err != nil {
		t.Fatalf("waitGone() error = %v", err)
	}
}

// stubRecloner records whether the storage domain was asked to move anything.
type stubRecloner struct{ recloned int }

func (s *stubRecloner) CheckReclone(context.Context, string, string) error { return nil }
func (s *stubRecloner) Reclone(context.Context, string, string) (Reclone, error) {
	s.recloned++
	return Reclone{}, nil
}

// A stopped jail that stays dying is refused with 409 before anything moves,
// and the reason reaches the client with the recovery information (#281).
func TestRecloneRefusesAJailThatStaysDying(t *testing.T) {
	recloner := &stubRecloner{}
	service := NewJailService(Dependencies{
		System:   listSystem{jails: []Jail{{Name: "db1", Status: JailStatusStopped, Path: "/jails/db1"}}},
		Audit:    &fakeLogger{},
		Recloner: recloner,
	})
	asked := 0
	service.SetRemoval(removalAfter{asked: &asked})
	service.goneWait, service.gonePoll = 20*time.Millisecond, time.Millisecond

	router := chi.NewRouter()
	RegisterJailRoutes(router, NewJailHandler(service))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/jails/db1/reclone?confirm=db1",
		strings.NewReader(`{"template":"p/templates/t@new"}`)))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
	var got Reclone
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.FailedAt != "stop" || !strings.Contains(got.Error, "still dying") {
		t.Errorf("body = %+v, want failed_at stop and the reason", got)
	}
	if recloner.recloned != 0 {
		t.Error("the storage domain was asked to move a jail that was still dying")
	}
}
