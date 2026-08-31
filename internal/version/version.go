// Package version reports the build identity of the running binary, so a bug
// report from a source-only release can name the exact build it came from.
package version

import "runtime/debug"

// version is stamped by the build:
//
//	go build -ldflags "-X k8s-cex-dra-driver/internal/version.version=<v>"
//
// make derives <v> from git describe. Unstamped builds (plain go build, a
// build context without .git) fall back to Go's own VCS metadata below.
var version = ""

// Get returns the stamped version, or the VCS revision Go embedded at build
// time, or "unknown". It never returns an empty string, so log lines and
// --version output always carry something greppable.
func Get() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var revision, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if modified == "true" {
			return "git-" + revision + "-dirty"
		}
		return "git-" + revision
	}
	// Module version is "(devel)" for every source build, so it only helps
	// when the binary was built as a versioned module dependency.
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "unknown"
}
