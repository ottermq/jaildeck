package jails

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// A jail Ensure made, so its stanza is the one Jail Deck writes.
func configuredHost(t *testing.T) (*ensureHost, *JailService) {
	t.Helper()
	host := newEnsureHost()
	service := host.service()
	if _, err := service.Ensure(context.Background(), "db1", validSpec()); err != nil {
		t.Fatal(err)
	}
	return host, service
}

func TestTheConfigurationIsTheStanzaAndTheRulesOnTheHost(t *testing.T) {
	host, service := configuredHost(t)
	host.rctl.rules = map[string][]string{"db1": {"jail:db1:pcpu:deny=200", "jail:db1:maxproc:deny=300"}}

	got, err := service.Configuration(context.Background(), "db1")
	if err != nil {
		t.Fatal(err)
	}
	stanza, _ := host.reader.content("db1")
	if got.Name != "db1" || got.File.Path != "/etc/jail.conf.d/db1.conf" || !got.File.Present ||
		got.File.Content != stanza || got.File.Truncated {
		t.Errorf("file = %+v", got.File)
	}
	if len(got.Rules) != 2 || got.Rules[0] != "jail:db1:pcpu:deny=200" || got.RulesError != "" {
		t.Errorf("rules = %v, %q", got.Rules, got.RulesError)
	}

	// Edited by hand, it is what the file says now, not what was written.
	host.edit("db1", func(s string) string { return s + "# changed by hand\n" })
	if again, _ := service.Configuration(context.Background(), "db1"); !strings.HasSuffix(again.File.Content, "# changed by hand\n") {
		t.Errorf("content = %q", again.File.Content)
	}
}

func TestAJailWithNoRulesHasAnEmptyList(t *testing.T) {
	_, service := configuredHost(t)
	got, err := service.Configuration(context.Background(), "db1")
	if err != nil || got.Rules == nil || len(got.Rules) != 0 {
		t.Errorf("rules = %#v, %v", got.Rules, err)
	}
}

func TestRulesThatCannotBeReadAreSaidSo(t *testing.T) {
	host, service := configuredHost(t)
	host.rctl.listErr = errors.New("RACCT/RCTL present, but disabled")
	got, err := service.Configuration(context.Background(), "db1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Rules != nil || !strings.Contains(got.RulesError, "disabled") || !got.File.Present {
		t.Errorf("got = %+v", got)
	}
}

func TestAJailWithoutItsOwnFileIsAnsweredWithThePath(t *testing.T) {
	host, service := configuredHost(t)
	host.reader.absent = true
	got, err := service.Configuration(context.Background(), "db1")
	if err != nil {
		t.Fatal(err)
	}
	if got.File.Present || got.File.Content != "" || got.File.Path != "/etc/jail.conf.d/db1.conf" {
		t.Errorf("file = %+v", got.File)
	}
}

func TestTheConfigurationOfNoSuchJailIs404(t *testing.T) {
	_, service := configuredHost(t)
	_, err := service.Configuration(context.Background(), "db9")
	wantCode(t, err, http.StatusNotFound, "not found")
}

func TestALongFileIsCutOnACharacter(t *testing.T) {
	content := strings.Repeat("a", MaxShownFile-1) + "é" + "tail"
	got, cut := shown(content)
	if !cut || len(got) != MaxShownFile-1 {
		t.Errorf("cut %v, len %d", cut, len(got))
	}
	if got, cut := shown("short"); cut || got != "short" {
		t.Errorf("%q, %v", got, cut)
	}
}
