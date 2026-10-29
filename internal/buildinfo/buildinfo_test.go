package buildinfo

import (
	"runtime/debug"
	"testing"
)

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

func TestCommitReadsTheVCSStamp(t *testing.T) {
	revision, modified := commitOf([]debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "4279f936b78e"},
		{Key: "vcs.modified", Value: "true"},
	})
	if revision != "4279f936b78e" || !modified {
		t.Errorf("commitOf = %q, %v", revision, modified)
	}
	if revision, modified := commitOf(nil); revision != "" || modified {
		t.Errorf("no stamp = %q, %v", revision, modified)
	}
}
