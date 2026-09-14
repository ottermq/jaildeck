package buildinfo

import "testing"

func TestVersion_InjectedValueWins(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v1.2.3"
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want %q", got, "v1.2.3")
	}
}

func TestVersion_FallbackIsNeverEmpty(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = ""
	if got := Version(); got == "" {
		t.Errorf("Version() returned empty string")
	}
}
