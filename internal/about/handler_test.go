package about

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ottermq/jaildeck/internal/buildinfo"
)

func TestAboutSaysWhatJaildeckVersionSays(t *testing.T) {
	started := time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)
	r := chi.NewRouter()
	RegisterAboutRoutes(r, NewHandler(started, "/var/db/jaildeck"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/about", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got About
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	commit, modified := buildinfo.Commit()
	want := About{Version: buildinfo.Version(), Commit: commit, Modified: modified, Started: started, StateDir: "/var/db/jaildeck"}
	if got != want {
		t.Errorf("about = %+v\nwant    %+v", got, want)
	}
	if got.Version == "" {
		t.Error("no version")
	}
}
