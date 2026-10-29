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

// Commit is the git revision the Go toolchain stamped into the binary, and
// whether the tree had uncommitted changes. Empty when it stamped none: a
// build outside a checkout, or with -buildvcs=false.
func Commit() (revision string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	return commitOf(info.Settings)
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
