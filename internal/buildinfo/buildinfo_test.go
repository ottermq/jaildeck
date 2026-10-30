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

// A package build injects the commit, because the toolchain stamps none in
// CI's VM (#165).
func TestCommit_InjectedValueIsTheFallback(t *testing.T) {
	if info, ok := debug.ReadBuildInfo(); ok {
		if stamped, _ := commitOf(info.Settings); stamped != "" {
			t.Skip("this test binary carries a VCS stamp, which wins")
		}
	}
	old := commit
	t.Cleanup(func() { commit = old })

	commit = "6c7251b0c0ffee0000000000000000000000beef"
	if revision, modified := Commit(); revision != commit || modified {
		t.Errorf("Commit() = %q, %v", revision, modified)
	}
	commit = ""
	if revision, _ := Commit(); revision != "" {
		t.Errorf("Commit() with nothing = %q", revision)
	}
}
