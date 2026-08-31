// Package binding switches AP queues between the zcrypt and vfio-ap drivers
// using the driver_override sysfs mechanism.
package binding

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s-cex-dra-driver/internal/logphase"
	"k8s-cex-dra-driver/internal/sysfs"
)

// DriversProbePath is the bus-wide attribute that re-probes a queue after its
// driver_override changed. A var, not a const, so a test can point the package
// at a fake sysfs tree. Nothing in production reassigns it.
var DriversProbePath = "/sys/bus/ap/drivers_probe"

// sysfsWriteMode is the mode passed to os.WriteFile for sysfs
// attributes. The kernel ignores it. 0o600 satisfies gosec G306.
const sysfsWriteMode os.FileMode = 0o600

// detailLevel is the klog verbosity at which a switch narrates its individual
// sysfs writes and reads back what it wrote. Untyped so the package needs no
// klog import. Matches the scanner's per-item detail level. Each switch
// reports one line at default verbosity, which matters because the startup
// drain switches every orphaned queue on the node.
const detailLevel = 4

// driverOverridePath returns the driver_override sysfs path for an AP queue.
func driverOverridePath(apid, apqi string) string {
	return filepath.Join(sysfs.APDevicesPath, fmt.Sprintf("card%s", apid), fmt.Sprintf("%s.%s", apid, apqi), "driver_override")
}

// deviceUnbindPath returns the device-level driver/unbind sysfs path for an AP queue.
func deviceUnbindPath(apid, apqi string) string {
	return filepath.Join(sysfs.APDevicesPath, fmt.Sprintf("card%s", apid), fmt.Sprintf("%s.%s", apid, apqi), "driver", "unbind")
}

// unbindQueue unbinds an AP queue from its current driver. A queue bound to
// no driver has no `driver` symlink, so ENOENT means there is nothing to
// unbind - already the state this write exists to reach. Queues start exactly
// so on nodes whose apmask/aqmask were prepared before the driver ever ran,
// and a switch interrupted after its own unbind leaves the same shape behind.
// Failing on ENOENT made both unswitchable for good.
func unbindQueue(queueID, apid, apqi string) error {
	err := os.WriteFile(deviceUnbindPath(apid, apqi), []byte(queueID), sysfsWriteMode)
	if errors.Is(err, os.ErrNotExist) {
		logphase.Logf(logphase.Preparation, "%s: bound to no driver, nothing to unbind", queueID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("unbind %s from current driver: %w", queueID, err)
	}
	return nil
}

// SwitchToVFIOAP switches a single AP queue from zcrypt to vfio-ap
// using the driver_override sysfs mechanism.
//
// The override is written before the unbind on purpose: an unbound queue with
// an empty driver_override belongs to no driver pool and no recovery path can
// tell it from one an admin detached deliberately, while driver_override ==
// "vfio_ap" is exactly what the startup drain reclaims. Ordered this way, a
// switch that dies at any step leaves the queue either untouched on zcrypt or
// carrying the drain's marker - never the invisible shape. The override write
// itself rebinds nothing. Only the probe acts on it.
func SwitchToVFIOAP(apid, apqi string) error {
	queueID := fmt.Sprintf("%s.%s", apid, apqi)

	if err := os.WriteFile(driverOverridePath(apid, apqi), []byte("vfio_ap"), sysfsWriteMode); err != nil {
		return fmt.Errorf("write driver_override for %s: %w", queueID, err)
	}

	if err := unbindQueue(queueID, apid, apqi); err != nil {
		return err
	}

	if err := os.WriteFile(DriversProbePath, []byte(queueID), sysfsWriteMode); err != nil {
		return fmt.Errorf("drivers_probe %s: %w", queueID, err)
	}

	logphase.Logf(logphase.Preparation, "Switched %s to vfio-ap via driver_override", queueID)
	return nil
}

// logOverride reads a queue's driver_override back and reports it at
// detailLevel, when being "before" or "after" the clear. The read serves the
// log line and nothing else - no caller acts on the value - so it is skipped
// entirely unless the operator asked for the detail.
func logOverride(queueID, overridePath, when string) {
	if !logphase.VEnabled(detailLevel) {
		return
	}
	cur, err := sysfs.ReadFile(overridePath)
	if err != nil {
		logphase.Vf(detailLevel, logphase.Preparation, "%s: cannot read driver_override %s clear: %v", queueID, when, err)
		return
	}
	logphase.Vf(detailLevel, logphase.Preparation, "%s: driver_override %s clear: %q (len=%d)", queueID, when, cur, len(cur))
}

// SwitchToZcrypt switches a single AP queue from vfio-ap back to zcrypt.
func SwitchToZcrypt(apid, apqi string) error {
	queueID := fmt.Sprintf("%s.%s", apid, apqi)

	// Step 1: unbind from current driver
	if err := unbindQueue(queueID, apid, apqi); err != nil {
		return err
	}

	// Step 2: clear driver_override
	overridePath := driverOverridePath(apid, apqi)
	logOverride(queueID, overridePath, "before")

	logphase.Vf(detailLevel, logphase.Preparation, "%s: clearing driver_override - writing %q to %s", queueID, "\n", overridePath)
	if err := os.WriteFile(overridePath, []byte("\n"), sysfsWriteMode); err != nil {
		return fmt.Errorf("clear driver_override for %s: %w", queueID, err)
	}

	logOverride(queueID, overridePath, "after")

	// Step 3: trigger drivers_probe
	logphase.Vf(detailLevel, logphase.Preparation, "%s: probing - writing %q to %s", queueID, queueID, DriversProbePath)
	if err := os.WriteFile(DriversProbePath, []byte(queueID), sysfsWriteMode); err != nil {
		// The queue is unbound and its override was just cleared: the one
		// shape no recovery path can see. Put the vfio_ap marker back so the
		// startup drain retries this switch instead of losing the queue from
		// both driver pools until reboot. Best-effort - the probe error is
		// the one worth returning.
		if mErr := os.WriteFile(overridePath, []byte("vfio_ap"), sysfsWriteMode); mErr != nil {
			logphase.Logf(logphase.Preparation, "Warning: restore of drain marker for %s failed: %v", queueID, mErr)
		}
		return fmt.Errorf("drivers_probe %s: %w", queueID, err)
	}

	logphase.Logf(logphase.Preparation, "Switched %s back to zcrypt via driver_override", queueID)
	return nil
}
