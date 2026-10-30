package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ottermq/jaildeck/internal/common"
	"github.com/ottermq/jaildeck/internal/state"
)

func TestParseScopes(t *testing.T) {
	got, err := ParseScopes([]string{"console", " console "})
	if err != nil || !reflect.DeepEqual(got, []string{"console"}) {
		t.Errorf("ParseScopes = %q, %v; want console once", got, err)
	}
	if got, err := ParseScopes(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("ParseScopes(nil) = %#v, %v; want an empty list, not nil", got, err)
	}
	if _, err := ParseScopes([]string{"consol"}); err == nil || !strings.Contains(err.Error(), "the scopes are console") {
		t.Errorf("a typo: %v, want it refused, naming the scopes", err)
	}
}

// A token from before scopes keeps every one; a token made since has what it
// was given (#178).
func TestTokenHas(t *testing.T) {
	for _, tt := range []struct {
		name   string
		scopes []string
		want   bool
	}{
		{"made before scopes", nil, true},
		{"made with none", []string{}, false},
		{"made with console", []string{"console"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := TokenHas(state.Token{Name: "t", Scopes: tt.scopes}, ScopeConsole); got != tt.want {
				t.Errorf("TokenHas = %t, want %t", got, tt.want)
			}
		})
	}
}

// The state file keeps no list and an empty list apart, through a save and a
// load, which is the whole difference between an old token and a new one.
func TestScopesSurviveTheStateFile(t *testing.T) {
	dir := t.TempDir()
	tokens := []state.Token{
		{Name: "old", Hash: "a", CreatedAt: time.Unix(1, 0).UTC()},
		{Name: "none", Hash: "b", CreatedAt: time.Unix(2, 0).UTC(), Scopes: []string{}},
		{Name: "console", Hash: "c", CreatedAt: time.Unix(3, 0).UTC(), Scopes: []string{"console"}},
	}
	store := state.NewStore(dir)
	if err := store.Save(state.State{APITokens: tokens}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, false, true}
	for i, token := range st.APITokens {
		if TokenHas(token, ScopeConsole) != want[i] {
			t.Errorf("%s after a load: console = %t, want %t (scopes %#v)", token.Name, !want[i], want[i], token.Scopes)
		}
	}
}

func TestAllow(t *testing.T) {
	session := Session{ExpiresAt: time.Now().Add(time.Hour)}
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		without []Scope
		want    int
		says    string
	}{
		{name: "old token", ctx: context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "old"})},
		{name: "token with console",
			ctx: context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "otter", Scopes: []string{"console"}})},
		{name: "token without", ctx: context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "ci", Scopes: []string{}}),
			want: http.StatusForbidden, says: `the token "ci" does not have the console scope`},
		{name: "session", ctx: context.WithValue(context.Background(), sessionKey{}, session)},
		{name: "session, console off", ctx: context.WithValue(context.Background(), sessionKey{}, session),
			without: []Scope{ScopeConsole}, want: http.StatusForbidden, says: "JAILDECK_CONSOLE_UI=off"},
		{name: "token, console off for the browser",
			ctx:     context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "otter", Scopes: []string{"console"}}),
			without: []Scope{ScopeConsole}},
		{name: "nobody", ctx: context.Background(), want: http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(nil)
			h.SetSessionWithout(tt.without...)
			err := h.Allow(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(tt.ctx), ScopeConsole)
			if tt.want == 0 {
				if err != nil {
					t.Errorf("Allow = %v, want allowed", err)
				}
				return
			}
			var appErr *common.AppError
			if !errors.As(err, &appErr) || appErr.Code != tt.want || !strings.Contains(appErr.Message, tt.says) {
				t.Errorf("Allow = %v, want %d saying %q", err, tt.want, tt.says)
			}
		})
	}
}

// /auth/me answers what the principal may do, so the interface can leave
// out the console where it would be refused.
func TestMeAnswersScopes(t *testing.T) {
	session := Session{ExpiresAt: time.Now().Add(time.Hour)}
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		without []Scope
		want    []Scope
	}{
		{"session", context.WithValue(context.Background(), sessionKey{}, session), nil, []Scope{ScopeConsole}},
		{"session, console off", context.WithValue(context.Background(), sessionKey{}, session), []Scope{ScopeConsole}, []Scope{}},
		{"old token", context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "old"}), nil, []Scope{ScopeConsole}},
		{"new token, none", context.WithValue(context.Background(), bearerKey{}, state.Token{Name: "ci", Scopes: []string{}}), nil, []Scope{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewAuthHandler(nil)
			h.SetSessionWithout(tt.without...)
			rec := httptest.NewRecorder()
			h.Me(rec, httptest.NewRequest(http.MethodGet, "/auth/me", nil).WithContext(tt.ctx))
			var me MeResponse
			if err := json.NewDecoder(rec.Body).Decode(&me); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(me.Principal.Scopes, tt.want) {
				t.Errorf("scopes = %#v, want %#v", me.Principal.Scopes, tt.want)
			}
		})
	}
}
