package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// driverOptionsDoc is the reference page documenting the binary's flag
// surface, relative to this package.
const driverOptionsDoc = "../../docs/reference/driver-options.md"

// TestDriverOptionsDocMatchesRegisteredFlags guards the documented flag table
// against drift in both directions: a flag added to the binary without a table
// row, and a table row for a flag that no longer exists.
//
// Parsing is deliberately minimal: a table row counts as documenting a flag
// when its first cell is a backticked token starting with a dash. Names are
// compared with the dashes stripped, so `-v` and `--vmodule` both normalize.
// Defaults and wording are maintained by hand and re-read against --help in
// the release docs pass. Only the flag-name sets are checked here.
// CEX_DRA_SKIP_PREFLIGHT has no flag equivalent and its section is prose, so
// it never enters the comparison.
func TestDriverOptionsDocMatchesRegisteredFlags(t *testing.T) {
	raw, err := os.ReadFile(driverOptionsDoc) //nolint:gosec // fixed in-repo docs path
	if err != nil {
		t.Fatalf("read %s: %v", driverOptionsDoc, err)
	}

	rowRE := regexp.MustCompile("(?m)^\\| `(-{1,2}[a-z][a-z-]*)`")
	documented := make(map[string]bool)
	for _, m := range rowRE.FindAllStringSubmatch(string(raw), -1) {
		documented[strings.TrimLeft(m[1], "-")] = true
	}
	if len(documented) == 0 {
		t.Fatalf("no flag rows parsed from %s: the table or the parser broke", driverOptionsDoc)
	}

	registered := make(map[string]bool)
	for _, f := range newApp().Flags {
		registered[f.Names()[0]] = true
	}

	for name := range registered {
		if !documented[name] {
			t.Errorf("flag --%s is registered by the binary but has no row in %s", name, driverOptionsDoc)
		}
	}
	for name := range documented {
		if !registered[name] {
			t.Errorf("%s documents flag --%s, which the binary does not register", driverOptionsDoc, name)
		}
	}
}
