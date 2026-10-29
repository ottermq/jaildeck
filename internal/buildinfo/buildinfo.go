package buildinfo

import "runtime/debug"

// version is injected for release and package builds:
//
//	-ldflags "-X github.com/ottermq/jaildeck/internal/buildinfo.version=vX.Y.Z"
var version string

// Version reports the binary's version. A link-time value wins; otherwise it
// uses what the Go toolchain stamped from git.
func Version() string {
	if version != "" {
		return version
	}

	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// commit is injected the same way for package builds (#165):
//
//	-ldflags "-X github.com/ottermq/jaildeck/internal/buildinfo.commit=<sha>"
//
// The package is built where the toolchain may not stamp one -- in CI's
// FreeBSD VM, or from a `git archive` with no repository at all -- and v0.20.0's
// package reported none.
var commit string

// Commit is the git revision the binary was built from, and whether the tree
// had uncommitted changes. What the Go toolchain stamped wins, because it
// read the tree itself; the injected value is the fallback, and says nothing
// about changes. Empty when there is neither.
func Commit() (revision string, modified bool) {
	if info, ok := debug.ReadBuildInfo(); ok {
		if revision, modified = commitOf(info.Settings); revision != "" {
			return revision, modified
		}
	}
	return commit, false
}

func commitOf(settings []debug.BuildSetting) (revision string, modified bool) {
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return revision, modified
}
