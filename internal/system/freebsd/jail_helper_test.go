package freebsd

import "testing"

func TestParseSingleJailConfResolvesParenNameVariable(t *testing.T) {
	jail, ok := parseJailConfDir(`
web {
	host.hostname = "$(name).example.test";
	path = "/usr/local/jails/$(name)";
	ip4 = "192.0.2.10";
}
`)
	if !ok {
		t.Fatal("expected jail conf to parse")
	}

	if jail.Name != "web" {
		t.Fatalf("expected jail name web, got %q", jail.Name)
	}
	if jail.Hostname != "web.example.test" {
		t.Fatalf("expected hostname to resolve $(name), got %q", jail.Hostname)
	}
	if jail.Path != "/usr/local/jails/web" {
		t.Fatalf("expected path to resolve $(name), got %q", jail.Path)
	}
}
