package version

import "testing"

// TestGetStamped pins the stamped path: an ldflags value wins over every
// fallback verbatim.
func TestGetStamped(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v1.0.0-alpha"
	if got := Get(); got != "v1.0.0-alpha" {
		t.Errorf("Get() = %q, want %q", got, "v1.0.0-alpha")
	}
}

// TestGetNeverEmpty pins the contract the log line and --version rely on:
// whatever the build embedded (test binaries carry no VCS metadata), Get
// reports something.
func TestGetNeverEmpty(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = ""
	if got := Get(); got == "" {
		t.Error("Get() = \"\", want a non-empty fallback")
	}
}
