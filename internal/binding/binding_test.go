package binding

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"k8s.io/klog/v2"

	"k8s-cex-dra-driver/internal/sysfs"
)

// withVerbosity raises klog verbosity for one test and restores it after.
func withVerbosity(t *testing.T, level int) {
	t.Helper()
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	if err := fs.Set("v", strconv.Itoa(level)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := fs.Set("v", "0"); err != nil {
			t.Error(err)
		}
	})
}

// removeDriverLink strips a queue's driver directory, giving it the shape of
// a queue bound to no driver: no `driver` symlink, so no unbind attribute.
func removeDriverLink(t *testing.T, root, queue string) {
	t.Helper()
	apid, _, _ := splitQueue(queue)
	if err := os.RemoveAll(filepath.Join(root, "card"+apid, queue, "driver")); err != nil {
		t.Fatalf("remove driver dir for %s: %v", queue, err)
	}
}

// readProbe returns what was last written to the bus-wide drivers_probe.
func readProbe(t *testing.T) string {
	t.Helper()
	got, err := sysfs.ReadFile(DriversProbePath)
	if err != nil {
		t.Fatalf("read drivers_probe: %v", err)
	}
	return got
}

// TestSwitchToVFIOAPUnboundQueue pins the switch onto a queue bound to no
// driver: nodes whose apmask/aqmask were prepared before the driver ever ran
// start their queues exactly so, and the unconditional unbind write used to
// fail every Prepare on them with ENOENT, permanently.
func TestSwitchToVFIOAPUnboundQueue(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": ""})
	removeDriverLink(t, root, "01.0002")

	if err := SwitchToVFIOAP("01", "0002"); err != nil {
		t.Fatalf("SwitchToVFIOAP: %v", err)
	}
	if got := readOverride(t, root, "01.0002"); got != "vfio_ap" {
		t.Errorf("driver_override = %q, want %q", got, "vfio_ap")
	}
	if got := readProbe(t); got != "01.0002" {
		t.Errorf("drivers_probe = %q, want %q (probe must still run)", got, "01.0002")
	}
}

// TestSwitchToVFIOAPBoundQueue pins the common case: a queue bound to zcrypt
// is unbound first, then overridden and probed.
func TestSwitchToVFIOAPBoundQueue(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": ""})

	if err := SwitchToVFIOAP("01", "0002"); err != nil {
		t.Fatalf("SwitchToVFIOAP: %v", err)
	}
	unbind, err := sysfs.ReadFile(filepath.Join(root, "card01", "01.0002", "driver", "unbind"))
	if err != nil {
		t.Fatalf("read unbind: %v", err)
	}
	if unbind != "01.0002" {
		t.Errorf("unbind = %q, want %q", unbind, "01.0002")
	}
	if got := readOverride(t, root, "01.0002"); got != "vfio_ap" {
		t.Errorf("driver_override = %q, want %q", got, "vfio_ap")
	}
}

// TestSwitchToVFIOAPOverrideFailureLeavesQueueBound pins the step order that
// keeps a failed switch recoverable: the override write comes first, so when
// it fails the queue has not been unbound and stays on zcrypt. Unbinding
// first would leave an unbound queue with an empty override - a shape no
// recovery path can see.
func TestSwitchToVFIOAPOverrideFailureLeavesQueueBound(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": ""})
	// driver_override as a directory: the write fails without touching it.
	overridePath := filepath.Join(root, "card01", "01.0002", "driver_override")
	if err := os.Remove(overridePath); err != nil {
		t.Fatalf("remove driver_override: %v", err)
	}
	if err := os.MkdirAll(overridePath, 0o750); err != nil {
		t.Fatalf("mkdir driver_override: %v", err)
	}

	if err := SwitchToVFIOAP("01", "0002"); err == nil {
		t.Fatal("SwitchToVFIOAP succeeded with an unwritable driver_override")
	}
	unbind, err := sysfs.ReadFile(filepath.Join(root, "card01", "01.0002", "driver", "unbind"))
	if err != nil {
		t.Fatalf("read unbind: %v", err)
	}
	if unbind != "" {
		t.Errorf("unbind = %q, want untouched (queue must stay bound to zcrypt)", unbind)
	}
}

// TestSwitchToZcryptProbeFailureRestoresDrainMarker pins the one dangerous
// window on the way back: after the override is cleared and before the probe
// binds the queue, a probe failure would leave the invisible unbound-and-
// unmarked shape. The switch must put the vfio_ap marker back so the startup
// drain retries instead of losing the queue until reboot.
func TestSwitchToZcryptProbeFailureRestoresDrainMarker(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": "vfio_ap"})
	removeDriverLink(t, root, "01.0002")
	// A probe path inside a missing directory: the write fails.
	t.Cleanup(swap(&DriversProbePath, filepath.Join(root, "no-such-dir", "drivers_probe")))

	if err := SwitchToZcrypt("01", "0002"); err == nil {
		t.Fatal("SwitchToZcrypt succeeded with an unwritable drivers_probe")
	}
	if got := readOverride(t, root, "01.0002"); got != "vfio_ap" {
		t.Errorf("driver_override = %q, want %q restored (drain marker)", got, "vfio_ap")
	}
}

// TestSwitchToZcryptUnboundQueue pins the way back for the same shape: a
// switch interrupted right after its own unbind leaves the queue unbound with
// driver_override still set, and returning it to zcrypt must not trip over
// the missing driver symlink.
func TestSwitchToZcryptUnboundQueue(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": "vfio_ap"})
	removeDriverLink(t, root, "01.0002")

	if err := SwitchToZcrypt("01", "0002"); err != nil {
		t.Fatalf("SwitchToZcrypt: %v", err)
	}
	if got := readOverride(t, root, "01.0002"); got != "" {
		t.Errorf("driver_override = %q, want cleared", got)
	}
	if got := readProbe(t); got != "01.0002" {
		t.Errorf("drivers_probe = %q, want %q (probe must still run)", got, "01.0002")
	}
}

// TestSwitchToZcryptAtDetailVerbosity walks the same switch with the detail
// logging on. That path reads driver_override back twice purely to log it, and
// the reads are skipped at default verbosity, so nothing else exercises them.
func TestSwitchToZcryptAtDetailVerbosity(t *testing.T) {
	withVerbosity(t, detailLevel)

	root := fakeSysfs(t, map[string]string{"01.0002": "vfio_ap"})

	if err := SwitchToZcrypt("01", "0002"); err != nil {
		t.Fatalf("SwitchToZcrypt: %v", err)
	}
	if got := readOverride(t, root, "01.0002"); got != "" {
		t.Errorf("driver_override = %q, want cleared", got)
	}
}
