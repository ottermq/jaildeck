package jails

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/ottermq/jaildeck/internal/common"
)

// stubMover answers every move with moved and err.
type stubMover struct {
	moved DatasetMove
	err   error
}

func (s *stubMover) Retain(context.Context, string, string, string) (DatasetMove, error) {
	return s.moved, s.err
}

func (s *stubMover) Restore(context.Context, string, string, string) (DatasetMove, error) {
	return s.moved, s.err
}

// A move that renamed and then failed a mount answers with where the
// dataset is now and how far the mounts got, not only the error (#294).
func TestAMoveThatFailedAfterItsRenameSaysWhereItIs(t *testing.T) {
	mover := &stubMover{
		moved: DatasetMove{From: "p/db1/data", To: "p/retained/kept", RemountedChildren: []string{"p/retained/kept/wal"}},
		err:   common.NewAppError(http.StatusInternalServerError, "could not mount p/retained/kept/wal/archive"),
	}
	service := NewJailService(Dependencies{
		System:       listSystem{jails: []Jail{{Name: "db1", Status: JailStatusStopped, Path: "/jails/db1"}}},
		Audit:        &fakeLogger{},
		DatasetMover: mover,
	})
	router := chi.NewRouter()
	RegisterJailRoutes(router, NewJailHandler(service))
	for _, call := range []struct{ path, body string }{
		{"/jails/db1/datasets/data/retain", `{"name":"kept"}`},
		{"/jails/db1/datasets/data/restore", `{"from":"kept"}`},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, call.path, strings.NewReader(call.body)))
		var got DatasetMove
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: body %s", call.path, rec.Body)
		}
		if rec.Code != http.StatusInternalServerError || got.To != "p/retained/kept" ||
			!slices.Equal(got.RemountedChildren, []string{"p/retained/kept/wal"}) || !strings.Contains(got.Error, "could not mount") {
			t.Errorf("%s: %d %s", call.path, rec.Code, rec.Body)
		}
	}

	// Before the rename nothing moved, and the plain error is the answer.
	mover.moved, mover.err = DatasetMove{}, errors.New("cannot unmount: busy")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/jails/db1/datasets/data/retain", strings.NewReader(`{"name":"kept"}`)))
	if strings.Contains(rec.Body.String(), `"to"`) {
		t.Errorf("a move that did not happen answered with a move: %s", rec.Body)
	}
}
