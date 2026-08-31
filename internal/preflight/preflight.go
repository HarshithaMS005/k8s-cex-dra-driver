// Package preflight runs host-local prerequisite checks at driver startup.
// All checks are read-only observers. Remediation lives elsewhere.
package preflight

import (
	"fmt"
	"os"
	"strings"

	"k8s.io/component-base/featuregate"

	"k8s-cex-dra-driver/internal/features"
	"k8s-cex-dra-driver/internal/logphase"
)

// SkipEnvVar names the environment variable that, when set to a recognized
// true value, downgrades failing checks from startup-aborting errors to
// warnings.
const SkipEnvVar = "CEX_DRA_SKIP_PREFLIGHT"

// Severity is the result a check's failing condition maps to.
type Severity int

const (
	// SeverityWarn maps a failing check to a non-fatal warning.
	SeverityWarn Severity = iota
	// SeverityError maps a failing check to a startup-aborting error.
	SeverityError
)

// Status is the outcome of running a single check.
type Status string

const (
	// StatusOK indicates the check passed.
	StatusOK Status = "ok"
	// StatusWarn indicates the check failed at warning severity.
	StatusWarn Status = "warn"
	// StatusError indicates the check failed at error severity.
	StatusError Status = "error"
)

// CheckFunc runs a single check and returns its Status (ok, warn, or error)
// and a detail line for the report. A body runs inline on the startup path,
// so it stays a local observation - a stat or a short read of sysfs - and
// never blocks on anything that could hang.
type CheckFunc func() (Status, string)

// check is one entry of the registry in checks.go. Severity caps how bad the
// body's verdict may get: an error from a SeverityWarn check is downgraded to
// a warning, while a warn passes through unchanged. A check may name the
// feature gate whose path it serves. With that gate disabled the check is
// skipped entirely - the administrator declared the node does not serve the
// path, so its prerequisites are not demanded. The zero value runs always.
type check struct {
	name     string
	severity Severity
	body     CheckFunc
	gate     featuregate.Feature
}

// FatalError signals that strict mode failed and main should exit with code 2.
type FatalError struct {
	Errors int
}

func (e *FatalError) Error() string {
	return fmt.Sprintf("preflight: %d prerequisite check(s) failed", e.Errors)
}

// Run executes every registered check, emits the report block, and returns
// a *FatalError if strict mode and at least one error remained.
func Run() error {
	skip, raw, recognized := skipModeFromEnv()
	if raw != "" && !recognized {
		logphase.Logf(logphase.Preflight, "ignoring unrecognized %s=%q (treating as strict)", SkipEnvVar, raw)
	}
	if skip {
		logphase.Logf(logphase.Preflight, "skip-mode active (%s=%s): all errors will be downgraded to warnings", SkipEnvVar, raw)
	}

	active, skipped := partitionByGate(registry)
	for _, sg := range skipped {
		logphase.Logf(logphase.Preflight, "%s disabled: skipping checks: %s", sg.gate, strings.Join(sg.names, ", "))
	}

	var nOK, nWarn, nErr int
	for _, c := range active {
		status, detail := runOne(c)
		if status == StatusError && skip {
			status = StatusWarn
		}
		switch status {
		case StatusOK:
			nOK++
		case StatusWarn:
			nWarn++
		case StatusError:
			nErr++
		}
		if detail == "" {
			logphase.Logf(logphase.Preflight, "%s: %s", c.name, status)
		} else {
			logphase.Logf(logphase.Preflight, "%s: %s (%s)", c.name, status, detail)
		}
	}

	suffix := ""
	if skip {
		suffix = " (skip-mode)"
	}
	logphase.Logf(logphase.Preflight, "summary: %d ok, %d warn, %d error%s", nOK, nWarn, nErr, suffix)

	if !skip && nErr > 0 {
		logphase.Logf(logphase.Preflight, "aborting: %d prerequisite check(s) failed (set %s=1 to continue anyway)", nErr, SkipEnvVar)
		return &FatalError{Errors: nErr}
	}
	return nil
}

// gateSkips pairs a disabled gate with the names of the checks it scopes.
type gateSkips struct {
	gate  featuregate.Feature
	names []string
}

// partitionByGate splits the registry into the checks that run and, per
// disabled gate in registry order, the names of the checks skipped under it.
// A skipped check's body never executes and its verdict never enters the
// summary counts: a path the administrator turned off makes no demands on
// the node, it only leaves the one skip line per gate in the report.
func partitionByGate(checks []check) (active []check, skipped []gateSkips) {
	index := map[featuregate.Feature]int{}
	for _, c := range checks {
		if c.gate == "" || features.Gate.Enabled(c.gate) {
			active = append(active, c)
			continue
		}
		i, ok := index[c.gate]
		if !ok {
			i = len(skipped)
			index[c.gate] = i
			skipped = append(skipped, gateSkips{gate: c.gate})
		}
		skipped[i].names = append(skipped[i].names, c.name)
	}
	return active, skipped
}

// runOne executes a single check with panic recovery, mapping the body's
// verdict into a final status using the check's default severity. The body
// runs inline, so a panic in it lands in the deferred recover below and the
// phase continues with the remaining checks.
func runOne(c check) (status Status, detail string) {
	defer func() {
		if r := recover(); r != nil {
			status, detail = StatusError, fmt.Sprintf("panic: %v", r)
		}
	}()

	s, d := c.body()
	return mapSeverity(s, c.severity), d
}

// mapSeverity converts a body-reported failure into the registered severity.
// "ok" stays "ok" and "warn" stays "warn". "error" is downgraded to "warn"
// when the check was registered with SeverityWarn.
func mapSeverity(reported Status, sev Severity) Status {
	if reported == StatusError && sev == SeverityWarn {
		return StatusWarn
	}
	return reported
}

func skipModeFromEnv() (skip bool, raw string, recognized bool) {
	raw = os.Getenv(SkipEnvVar)
	if raw == "" {
		return false, "", true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true, raw, true
	}
	return false, raw, false
}
