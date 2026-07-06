package freebsd

import "testing"

func TestParseSingleJailConfResolvesParenNameVariable(t *testing.T) {
	jails := parseJailConf(`)
web {
	host.hostname = "$(name).example.test";
	path = "/usr/local/jails/$(name)";
	ip4 = "192.0.2.10";
}
`)

	if len(jails) != 1 {
		t.Fatal("expected exactly one jail")
	}

	if jails[0].Name != "web" {
		t.Fatalf("expected jail name web, got %q", jails[0].Name)
	}
	if jails[0].Hostname != "web.example.test" {
		t.Fatalf("expected hostname to resolve $(name), got %q", jails[0].Hostname)
	}
	if jails[0].Path != "/usr/local/jails/web" {
		t.Fatalf("expected path to resolve $(name), got %q", jails[0].Path)
	}
}
