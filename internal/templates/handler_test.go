package templates

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func put(t *testing.T, h *TemplateHandler, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	RegisterTemplateRoutes(r, h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/templates/"+name, strings.NewReader(body)))
	return rec
}

// A refresh takes from and nothing else (#252).
func TestRefreshRequestShape(t *testing.T) {
	_, _, _, service := refreshable(BuiltDerived)
	h := NewTemplateHandler(service)
	for _, body := range []string{
		`{"refresh":true}`,
		`{"refresh":true,"from":"pg18","packages":["nginx"]}`,
		`{"refresh":true,"from":"pg18","release":"15.1-RELEASE"}`,
	} {
		if rec := put(t, h, "pg18-p7", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", body, rec.Code)
		}
	}
	rec := put(t, h, "pg18-p7", `{"refresh":true,"from":"pg18"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("refresh: %d %s, want 202", rec.Code, rec.Body)
	}
	waitIdle(t, service, "pg18-p7")
}
