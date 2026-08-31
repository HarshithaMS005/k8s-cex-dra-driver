package preflight

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s-cex-dra-driver/internal/features"
	"k8s-cex-dra-driver/internal/mdev"
)

// swapRegistry replaces the check registry for one test. The registry in
// checks.go holds the real host checks, which cannot run against the test
// host. Every Run test starts from a controlled registry instead. Run tests
// call t.Setenv and so never run in parallel, which is what makes swapping a
// package-level variable safe here.
func swapRegistry(t *testing.T, checks []check) {
	t.Helper()
	old := registry
	registry = checks
	t.Cleanup(func() { registry = old })
}

func staticCheck(s Status) CheckFunc {
	return func() (Status, string) { return s, "" }
}

// disableVirtualMachineWorkload flips the VirtualMachineWorkload gate off for
// one test. features.Set is process-global, so the cleanup restores the
// default-on beta state rather than leaving the gate off for tests that run
// after. Run tests call t.Setenv and so never run in parallel, which is what
// makes flipping the global gate safe here.
func disableVirtualMachineWorkload(t *testing.T) {
	t.Helper()
	if err := features.Set(string(features.VirtualMachineWorkload) + "=false"); err != nil {
		t.Fatalf("disable %s: %v", features.VirtualMachineWorkload, err)
	}
	t.Cleanup(func() {
		if err := features.Set(string(features.VirtualMachineWorkload) + "=true"); err != nil {
			t.Errorf("restore %s: %v", features.VirtualMachineWorkload, err)
		}
	})
}

func TestRunAllOK(t *testing.T) {
	t.Setenv(SkipEnvVar, "")
	swapRegistry(t, []check{{name: "ok", severity: SeverityError, body: staticCheck(StatusOK)}})

	if err := Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunStrictModeAbortsOnError(t *testing.T) {
	t.Setenv(SkipEnvVar, "")
	swapRegistry(t, []check{
		{name: "ok", severity: SeverityError, body: staticCheck(StatusOK)},
		{name: "broken", severity: SeverityError, body: staticCheck(StatusError)},
	})

	err := Run()
	var fatal *FatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("Run = %v, want *FatalError", err)
	}
	if fatal.Errors != 1 {
		t.Errorf("Errors = %d, want 1", fatal.Errors)
	}
}

func TestRunSkipModeDowngradesErrors(t *testing.T) {
	t.Setenv(SkipEnvVar, "1")
	swapRegistry(t, []check{{name: "broken", severity: SeverityError, body: staticCheck(StatusError)}})

	if err := Run(); err != nil {
		t.Fatalf("Run in skip mode: %v", err)
	}
}

func TestRunUnrecognizedSkipValueStaysStrict(t *testing.T) {
	t.Setenv(SkipEnvVar, "banana")
	swapRegistry(t, []check{{name: "broken", severity: SeverityError, body: staticCheck(StatusError)}})

	var fatal *FatalError
	if err := Run(); !errors.As(err, &fatal) {
		t.Fatalf("Run = %v, want *FatalError", err)
	}
}

func TestRunSkipsGatedChecksWhileGateDisabled(t *testing.T) {
	t.Setenv(SkipEnvVar, "")
	disableVirtualMachineWorkload(t)
	called := false
	swapRegistry(t, []check{
		{name: "ungated ok", severity: SeverityError, body: staticCheck(StatusOK)},
		{name: "gated broken", severity: SeverityError, gate: features.VirtualMachineWorkload, body: func() (Status, string) {
			called = true
			return StatusError, ""
		}},
	})

	// The gated check would abort strict mode. With its gate disabled it
	// must not run at all, so Run passes on the ungated check alone.
	if err := Run(); err != nil {
		t.Fatalf("Run with the check's gate disabled: %v", err)
	}
	if called {
		t.Error("gated check body ran, want it skipped")
	}
}

func TestRunGatedChecksRunWhileGateEnabled(t *testing.T) {
	t.Setenv(SkipEnvVar, "")
	swapRegistry(t, []check{
		{name: "gated broken", severity: SeverityError, gate: features.VirtualMachineWorkload, body: staticCheck(StatusError)},
	})

	// The gate defaults on, so the check stays active and its error still
	// aborts strict mode: a node meant to serve VMs fails loudly, never
	// silently degrades.
	var fatal *FatalError
	if err := Run(); !errors.As(err, &fatal) {
		t.Fatalf("Run = %v, want *FatalError from the active gated check", err)
	}
}

func TestPartitionByGate(t *testing.T) {
	disableVirtualMachineWorkload(t)
	checks := []check{
		{name: "a", severity: SeverityError, body: staticCheck(StatusOK)},
		{name: "b", severity: SeverityError, gate: features.VirtualMachineWorkload, body: staticCheck(StatusOK)},
		{name: "c", severity: SeverityWarn, body: staticCheck(StatusOK)},
		{name: "d", severity: SeverityError, gate: features.VirtualMachineWorkload, body: staticCheck(StatusOK)},
	}

	active, skipped := partitionByGate(checks)

	var activeNames []string
	for _, c := range active {
		activeNames = append(activeNames, c.name)
	}
	if got, want := strings.Join(activeNames, ","), "a,c"; got != want {
		t.Errorf("active = %q, want %q", got, want)
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped has %d gate group(s), want 1", len(skipped))
	}
	if skipped[0].gate != features.VirtualMachineWorkload {
		t.Errorf("skipped gate = %q, want %q", skipped[0].gate, features.VirtualMachineWorkload)
	}
	if got, want := strings.Join(skipped[0].names, ","), "b,d"; got != want {
		t.Errorf("skipped names = %q, want %q", got, want)
	}
}

func TestRunWarnSeverityNeverAborts(t *testing.T) {
	t.Setenv(SkipEnvVar, "")
	swapRegistry(t, []check{{name: "flaky", severity: SeverityWarn, body: staticCheck(StatusError)}})

	if err := Run(); err != nil {
		t.Fatalf("Run with only warn-severity failures: %v", err)
	}
}

func TestRunOneSeverityMapping(t *testing.T) {
	cases := []struct {
		name     string
		severity Severity
		reported Status
		want     Status
	}{
		{"ok passes through", SeverityError, StatusOK, StatusOK},
		{"warn passes through", SeverityError, StatusWarn, StatusWarn},
		{"error stays error", SeverityError, StatusError, StatusError},
		{"error downgraded by warn severity", SeverityWarn, StatusError, StatusWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := check{name: "c", severity: tc.severity, body: staticCheck(tc.reported)}
			if got, _ := runOne(c); got != tc.want {
				t.Errorf("runOne = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunOneRecoversPanic(t *testing.T) {
	c := check{name: "panicky", severity: SeverityError, body: func() (Status, string) {
		panic("boom")
	}}

	status, detail := runOne(c)
	if status != StatusError {
		t.Errorf("status = %q, want %q", status, StatusError)
	}
	if !strings.Contains(detail, "boom") {
		t.Errorf("detail = %q, want the panic value in it", detail)
	}
}

func TestSkipModeFromEnv(t *testing.T) {
	cases := []struct {
		raw        string
		skip       bool
		recognized bool
	}{
		{"", false, true},
		{"1", true, true},
		{"true", true, true},
		{"YES", true, true},
		{"  yes ", true, true},
		{"0", false, false},
		{"banana", false, false},
	}
	for _, tc := range cases {
		t.Run("value "+tc.raw, func(t *testing.T) {
			t.Setenv(SkipEnvVar, tc.raw)
			skip, raw, recognized := skipModeFromEnv()
			if skip != tc.skip || recognized != tc.recognized {
				t.Errorf("skipModeFromEnv() = (%v, %q, %v), want (%v, _, %v)", skip, raw, recognized, tc.skip, tc.recognized)
			}
		})
	}
}

func TestCheckPathPresent(t *testing.T) {
	dir := t.TempDir()

	if status, _ := checkPathPresent(dir)(); status != StatusOK {
		t.Errorf("existing path: status = %q, want %q", status, StatusOK)
	}
	status, detail := checkPathPresent(filepath.Join(dir, "missing"))()
	if status != StatusError {
		t.Errorf("missing path: status = %q, want %q", status, StatusError)
	}
	if !strings.Contains(detail, "does not exist") {
		t.Errorf("missing path: detail = %q, want an existence message", detail)
	}
}

func TestCheckVFIOAPMatrix(t *testing.T) {
	dir := t.TempDir()
	old := mdev.VFIOAPMatrixPath
	t.Cleanup(func() { mdev.VFIOAPMatrixPath = old })

	mdev.VFIOAPMatrixPath = dir
	if status, _ := checkVFIOAPMatrix(); status != StatusOK {
		t.Errorf("matrix present: status = %q, want %q", status, StatusOK)
	}

	mdev.VFIOAPMatrixPath = filepath.Join(dir, "missing")
	status, detail := checkVFIOAPMatrix()
	if status != StatusError {
		t.Errorf("matrix missing: status = %q, want %q", status, StatusError)
	}
	if !strings.Contains(detail, "vfio_ap") {
		t.Errorf("matrix missing: detail = %q, want the module named", detail)
	}
}

// swapQueueDirGlob points the queue-directory glob at a synthetic tree for
// one test. The real glob touches fixed sysfs paths that do not exist on the
// test host.
func swapQueueDirGlob(t *testing.T, glob string) {
	t.Helper()
	old := apQueueDirGlob
	apQueueDirGlob = glob
	t.Cleanup(func() { apQueueDirGlob = old })
}

func TestCheckDriverOverride(t *testing.T) {
	newQueueDir := func(t *testing.T) (root, queueDir string) {
		t.Helper()
		root = t.TempDir()
		queueDir = filepath.Join(root, "card04", "04.0007")
		if err := os.MkdirAll(queueDir, 0o755); err != nil {
			t.Fatalf("mkdir queue dir: %v", err)
		}
		return root, queueDir
	}

	t.Run("attribute present", func(t *testing.T) {
		root, queueDir := newQueueDir(t)
		override := filepath.Join(queueDir, "driver_override")
		if err := os.WriteFile(override, []byte("\n"), 0o644); err != nil {
			t.Fatalf("write driver_override: %v", err)
		}
		swapQueueDirGlob(t, filepath.Join(root, "card*", "*.*"))

		status, detail := checkDriverOverride()
		if status != StatusOK {
			t.Errorf("status = %q, want %q", status, StatusOK)
		}
		if !strings.Contains(detail, override) || !strings.Contains(detail, "kernel ") {
			t.Errorf("detail = %q, want the probed path and the kernel release", detail)
		}
	})

	t.Run("no queues is inconclusive", func(t *testing.T) {
		swapQueueDirGlob(t, filepath.Join(t.TempDir(), "card*", "*.*"))

		status, detail := checkDriverOverride()
		if status != StatusWarn {
			t.Errorf("status = %q, want %q", status, StatusWarn)
		}
		if !strings.Contains(detail, "no AP queues to probe") {
			t.Errorf("detail = %q, want the inconclusive message", detail)
		}
	})

	t.Run("queue without the attribute is definitive", func(t *testing.T) {
		root, queueDir := newQueueDir(t)
		swapQueueDirGlob(t, filepath.Join(root, "card*", "*.*"))

		status, detail := checkDriverOverride()
		if status != StatusError {
			t.Errorf("status = %q, want %q", status, StatusError)
		}
		if !strings.Contains(detail, queueDir) || !strings.Contains(detail, "lacks AP-bus driver_override") {
			t.Errorf("detail = %q, want the queue path and the missing-feature message", detail)
		}
	})
}

// swapReadMasks replaces the mask reader for one test. The real reader
// touches fixed sysfs paths that do not exist on the test host.
func swapReadMasks(t *testing.T, f func() (string, string, error)) {
	t.Helper()
	old := readMasks
	readMasks = f
	t.Cleanup(func() { readMasks = old })
}

func TestCheckZcryptMasks(t *testing.T) {
	allOnes := "0x" + strings.Repeat("f", 64)
	restricted := "0x7" + strings.Repeat("f", 62) + "e"
	allZeros := "0x" + strings.Repeat("0", 64)

	// The masks decide whether driver_override is writable at all, so both
	// have to be all-1s and either one alone can fail the node. The all-0s
	// apmask is the shape the manual vfio-ap runbook leaves behind.
	cases := []struct {
		name       string
		ap, aq     string
		want       Status
		wantDetail []string
	}{
		{"all-1s masks", allOnes, allOnes, StatusOK, []string{"apmask=" + truncateMask(allOnes), "aqmask=" + truncateMask(allOnes)}},
		{"restricted apmask", restricted, allOnes, StatusError, []string{"apmask=" + truncateMask(restricted), "EINVAL", maskResetCmd}},
		{"restricted aqmask", allOnes, restricted, StatusError, []string{"aqmask=" + truncateMask(restricted), "EINVAL", maskResetCmd}},
		{"all-0s apmask", allZeros, allOnes, StatusError, []string{"apmask=" + truncateMask(allZeros), "EINVAL", maskResetCmd}},
		{"unparseable mask", "not-a-mask", allOnes, StatusError, []string{"EINVAL"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			swapReadMasks(t, func() (string, string, error) { return tc.ap, tc.aq, nil })
			status, detail := checkZcryptMasks()
			if status != tc.want {
				t.Errorf("status = %q, want %q", status, tc.want)
			}
			for _, want := range tc.wantDetail {
				if !strings.Contains(detail, want) {
					t.Errorf("detail = %q, want it to contain %q", detail, want)
				}
			}
		})
	}

	t.Run("unreadable masks", func(t *testing.T) {
		swapReadMasks(t, func() (string, string, error) {
			return "", "", errors.New("open /sys/bus/ap/apmask: no such file or directory")
		})
		status, detail := checkZcryptMasks()
		if status != StatusError {
			t.Errorf("status = %q, want %q", status, StatusError)
		}
		if !strings.Contains(detail, "cannot read masks") {
			t.Errorf("detail = %q, want a read-failure message", detail)
		}
	})
}

// TestMaskAllOnes pins the width-agnostic parse: the kernel sizes the mask
// string from AP_DEVICES and AP_DOMAINS, so the helper counts f's rather than
// comparing against a blessed constant.
func TestMaskAllOnes(t *testing.T) {
	cases := []struct {
		mask string
		want bool
	}{
		{"0x" + strings.Repeat("f", 64), true},
		{"0x" + strings.Repeat("F", 64), true},
		{strings.Repeat("f", 64), true},
		{"0x" + strings.Repeat("f", 16), true},
		{"0x" + strings.Repeat("f", 63) + "e", false},
		{"0x" + strings.Repeat("0", 64), false},
		{"0x", false},
		{"", false},
		{"not-a-mask", false},
	}
	for _, tc := range cases {
		if got := maskAllOnes(tc.mask); got != tc.want {
			t.Errorf("maskAllOnes(%q) = %v, want %v", tc.mask, got, tc.want)
		}
	}
}

func TestUnixString(t *testing.T) {
	if got := unixString([]byte{'6', '.', '1', '9', 0, 'x', 'x'}); got != "6.19" {
		t.Errorf("unixString = %q, want %q", got, "6.19")
	}
	if got := unixString([]byte("6.19")); got != "6.19" {
		t.Errorf("unixString without NUL = %q, want %q", got, "6.19")
	}
}

func TestTruncateMask(t *testing.T) {
	if got := truncateMask("0xffff"); got != "0xffff" {
		t.Errorf("short mask = %q, want unchanged", got)
	}
	long := "0x" + strings.Repeat("f", 64)
	got := truncateMask(long)
	if got != long[:18]+"..." {
		t.Errorf("long mask = %q, want first 18 chars plus ellipsis", got)
	}
}
