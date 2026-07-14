// Package buildinfo exposes the version embedded by the Go module toolchain.
package buildinfo

import "runtime/debug"

// override may be set by release builds with:
//
//	-ldflags "-X github.com/awhitty/bb/internal/buildinfo.override=v0.1.0"
var override string

// Version returns the release tag for module installs and "dev" for local
// untagged builds.
func Version() string {
	if override != "" {
		return override
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
