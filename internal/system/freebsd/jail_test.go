package freebsd

import (
	"testing"
)

func TestParseJLSOutput_Success(t *testing.T) {
	input := `{"__version": "2", "jail-information": {"jail": [{"jid":"1","name":"goodiesdb","host.hostname":"goodiesdb", "ip4.addr": [],"path":"/usr/local/jails/containers/goodiesdb"}]}}`

	got, err := parseJLSOutput(input)
	if err != nil {
		t.Fatalf("parse JLS output: %v", err)
	}
	if len(got.JailInformation.Jails) != 1 {
		t.Fatalf("jail count = %d, want 1", len(got.JailInformation.Jails))
	}

	jail := got.JailInformation.Jails[0]
	if jail.Name != "goodiesdb" {
		t.Fatalf("jail name = %q, want %q", jail.Name, "goodiesdb")
	}

	if jail.JID != "1" {
		t.Fatalf("jid = %q, want %q", jail.JID, "1")
	}
	if jail.Hostname != "goodiesdb" {
		t.Fatalf("hostname = %q, want %q", jail.Hostname, "goodiesdb")
	}
	if jail.Path != "/usr/local/jails/containers/goodiesdb" {
		t.Fatalf("path = %q", jail.Path)
	}
}
