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
