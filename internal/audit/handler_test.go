package audit

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

// newTestHandler wires an AuditHandler over a fakeReader (declared in
// service_test.go) so handler behaviour can be exercised over real HTTP.
func newTestHandler(reader *fakeReader) *AuditHandler {
	return NewAuditHandler(NewAuditService(reader))
}

// doList issues GET /audit<query> against the handler and returns the response.
func doList(t *testing.T, h *AuditHandler, query string) *http.Response {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/audit"+query, nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	return rec.Result()
}

func TestAuditHandler_List_ReturnsJSONEntries(t *testing.T) {
	ts := time.Date(2026, 8, 17, 20, 45, 9, 0, time.UTC)
	reader := &fakeReader{entries: []Entry{{
		Timestamp: ts,
		Operation: "start",
		Target:    "ottermq",
		Command:   "service jail start ottermq",
		Success:   true,
	}}}

	resp := doList(t, newTestHandler(reader), "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	var got []Entry
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Target != "ottermq" || got[0].Operation != "start" || !got[0].Success {
		t.Errorf("entry = %#v, want start/ottermq/success", got[0])
	}
	if !got[0].Timestamp.Equal(ts) {
		t.Errorf("Timestamp = %v, want %v", got[0].Timestamp, ts)
	}
}

func TestAuditHandler_List_NormalizesLimit(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantLimit int
	}{
		{name: "absent falls back to default", query: "", wantLimit: defaultLimit},
		{name: "non-numeric falls back to default", query: "?limit=abc", wantLimit: defaultLimit},
		{name: "empty value falls back to default", query: "?limit=", wantLimit: defaultLimit},
		{name: "zero falls back to default", query: "?limit=0", wantLimit: defaultLimit},
		{name: "negative falls back to default", query: "?limit=-5", wantLimit: defaultLimit},
		{name: "in-range value is honoured", query: "?limit=17", wantLimit: 17},
		{name: "above max is clamped", query: "?limit=5000", wantLimit: maxLimit},
		{name: "exactly max is honoured", query: "?limit=200", wantLimit: maxLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{}

			resp := doList(t, newTestHandler(reader), tt.query)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			if reader.gotLimit != tt.wantLimit {
				t.Errorf("limit reaching reader = %d, want %d", reader.gotLimit, tt.wantLimit)
			}
		})
	}
}

func TestAuditHandler_List_BuildsFilterReachingReader(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		wantOperation string
		wantSuccess   *bool
		wantTargets   []string
	}{
		{name: "no filters", query: ""},
		{name: "operation start", query: "?operation=start", wantOperation: "start"},
		{name: "operation stop", query: "?operation=stop", wantOperation: "stop"},
		{name: "operation restart", query: "?operation=restart", wantOperation: "restart"},
		{name: "unknown operation is not forwarded", query: "?operation=bogus"},
		{name: "success true", query: "?success=true", wantSuccess: boolPtr(true)},
		{name: "success false", query: "?success=false", wantSuccess: boolPtr(false)},
		{name: "invalid success is not forwarded", query: "?success=maybe"},
		{name: "single target", query: "?targets=ottermq", wantTargets: []string{"ottermq"}},
		{
			name:        "multiple targets are split and trimmed",
			query:       "?targets=ottermq,%20nginx%20",
			wantTargets: []string{"ottermq", "nginx"},
		},
		{name: "empty targets value is not forwarded", query: "?targets="},
		{
			name:          "all filters combined",
			query:         "?operation=stop&success=false&targets=redis&limit=25",
			wantOperation: "stop",
			wantSuccess:   boolPtr(false),
			wantTargets:   []string{"redis"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{}

			resp := doList(t, newTestHandler(reader), tt.query)
			defer resp.Body.Close()

			got := reader.gotFilter

			if got.Operation != tt.wantOperation {
				t.Errorf("Operation = %q, want %q", got.Operation, tt.wantOperation)
			}
			if !slices.Equal(got.Targets, tt.wantTargets) {
				t.Errorf("Targets = %#v, want %#v", got.Targets, tt.wantTargets)
			}
			if (got.Success == nil) != (tt.wantSuccess == nil) {
				t.Fatalf("Success = %v, want %v", got.Success, tt.wantSuccess)
			}
			if got.Success != nil && *got.Success != *tt.wantSuccess {
				t.Errorf("Success = %v, want %v", *got.Success, *tt.wantSuccess)
			}
		})
	}
}

func TestAuditHandler_List_ReaderErrorIsInternalServerError(t *testing.T) {
	reader := &fakeReader{err: errors.New("boom")}

	resp := doList(t, newTestHandler(reader), "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if body["error"] != "internal server error" {
		t.Errorf("error = %q, want %q", body["error"], "internal server error")
	}
}
