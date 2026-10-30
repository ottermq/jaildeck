package jails

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
)

// stubChildren answers with result and records what it was asked to destroy.
type stubChildren struct {
	result DatasetDestroy
	asked  []string
}

func (s *stubChildren) DestroyChild(_ context.Context, jailPath, child string) (DatasetDestroy, error) {
	s.asked = append(s.asked, jailPath+" "+child)
	return s.result, nil
}

func childHost(status JailStatus, result DatasetDestroy) (*JailService, *stubChildren) {
	children := &stubChildren{result: result}
	return NewJailService(Dependencies{
		System:   listSystem{jails: []Jail{{Name: "db1", Status: status, Path: "/jails/db1"}}},
		Audit:    &fakeLogger{},
		Children: children,
	}), children
}

// The confirm and the running jail refuse before the storage domain is asked
// anything (#120).
func TestDestroyDatasetRefusals(t *testing.T) {
	tests := []struct {
		name    string
		status  JailStatus
		confirm string
		want    int
	}{
		{name: "no confirm", status: JailStatusStopped, want: http.StatusBadRequest},
		{name: "the jail's name as confirm", status: JailStatusStopped, confirm: "db1", want: http.StatusBadRequest},
		{name: "running", status: JailStatusRunning, confirm: "data", want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, children := childHost(tt.status, DatasetDestroy{Destroyed: true})
			_, err := service.DestroyDataset(context.Background(), JailName("db1"), "data", tt.confirm)
			var appErr *common.AppError
			if !errors.As(err, &appErr) || appErr.Code != tt.want {
				t.Errorf("error = %v, want a %d", err, tt.want)
			}
			if len(children.asked) != 0 {
				t.Errorf("asked to destroy %q, want nothing", children.asked)
			}
		})
	}
}

// A stopped jail's child is handed to the storage domain with the jail's
// path, and a refusal it answers is 409 with the result as the body.
func TestDestroyDatasetHandler(t *testing.T) {
	for _, tt := range []struct {
		name   string
		result DatasetDestroy
		want   int
	}{
		{name: "destroyed", result: DatasetDestroy{Dataset: "p/db1/data", Destroyed: true}, want: http.StatusOK},
		{name: "blocked", result: DatasetDestroy{Dataset: "p/db1/data",
			Blocked: &DatasetBlock{Reason: "refused", Holds: []string{"p/db1/data@one (keep)"}}}, want: http.StatusConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, children := childHost(JailStatusStopped, tt.result)
			router := chi.NewRouter()
			RegisterJailRoutes(router, NewJailHandler(service))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/jails/db1/datasets/data?confirm=data", nil))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			var got DatasetDestroy
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Dataset != "p/db1/data" {
				t.Errorf("body = %s, want the result", rec.Body)
			}
			if len(children.asked) != 1 || children.asked[0] != "/jails/db1 data" {
				t.Errorf("asked %q, want the jail's path and the child", children.asked)
			}
		})
	}
}
