// Package logphase provides the [PHASE] log prefix shared across the driver
// and the packages it drives, so every line lands in the same structured shape
// and the phase vocabulary has one definition rather than a literal per call.
package logphase

import (
	"fmt"

	"k8s.io/klog/v2"
)

// Phase tags, in startup order: process startup (build identity and
// configuration resolution, before any check can run), prerequisite checks,
// kubelet registration, driver initialization, the device scan loop,
// allocation decisions, and claim preparation.
const (
	Startup      = "STARTUP"
	Preflight    = "PREFLIGHT"
	Registration = "REGISTRATION"
	Init         = "INIT"
	ScanLoop     = "SCAN-LOOP"
	Allocate     = "ALLOCATE"
	Preparation  = "PREPARATION"
)

// Logf emits an info line prefixed with "[<phase>] " using klog.
func Logf(phase, format string, args ...any) {
	klog.InfoDepth(1, line(phase, format, args))
}

// Warnf emits a warning line prefixed with "[<phase>] ". Warnings are the
// half of the phase output that reports something the operator may need to
// act on, so they carry the same prefix as the info lines around them.
func Warnf(phase, format string, args ...any) {
	klog.WarningDepth(1, line(phase, format, args))
}

// Vf emits an info line prefixed with "[<phase>] " only when klog verbosity
// is at or above level, for detail that would otherwise repeat every cycle.
func Vf(level klog.Level, phase, format string, args ...any) {
	if !klog.V(level).Enabled() {
		return
	}
	klog.InfoDepth(1, line(phase, format, args))
}

// VEnabled reports whether Vf at this level would emit. Callers use it to skip
// work done solely to produce a log line - reading a sysfs attribute nothing
// else consumes, for instance.
func VEnabled(level klog.Level) bool {
	return klog.V(level).Enabled()
}

// line renders one prefixed message. It stays a plain helper rather than a
// logging call of its own so the *Depth callers above keep reporting their
// own caller's file and line.
func line(phase, format string, args []any) string {
	return fmt.Sprintf("[%s] %s", phase, fmt.Sprintf(format, args...))
}
