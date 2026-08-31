package binding

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"k8s-cex-dra-driver/internal/logphase"
	"k8s-cex-dra-driver/internal/mdev"
	"k8s-cex-dra-driver/internal/sysfs"
)

// apQueueOverrideGlob enumerates every AP queue's driver_override. Rooted at
// sysfs.APDevicesPath, the same root driverOverridePath writes through, rather
// than the /sys/bus/ap/devices symlink view: both reach the same attributes for
// the same queues, and one root keeps the package's sysfs surface single.
func apQueueOverrideGlob() string {
	return filepath.Join(sysfs.APDevicesPath, "card*", "*.*", "driver_override")
}

// DrainOrphanedVFIOAP resets queues whose driver_override is vfio_ap but
// that no live mdev claims, restoring them to zcrypt. Recovers from a
// driver crash mid-Unprepare. Best-effort per queue: those errors are
// logged, never returned. The drain aborts with an error only when the
// set of live mdevs cannot be determined.
func DrainOrphanedVFIOAP() error {
	live, err := liveAPQNs()
	if err != nil {
		return fmt.Errorf("determine live vfio-ap queues: %w", err)
	}

	matches, err := filepath.Glob(apQueueOverrideGlob())
	if err != nil {
		return fmt.Errorf("glob driver_override paths: %w", err)
	}

	for _, p := range matches {
		raw, err := sysfs.ReadFile(p)
		if err != nil {
			// Queue vanished between glob and read (hot unplug):
			// nothing left to drain. Anything else is worth a line.
			if !errors.Is(err, fs.ErrNotExist) {
				logphase.Warnf(logphase.Preparation, "read %s: %v", p, err)
			}
			continue
		}
		if strings.TrimSpace(raw) != "vfio_ap" {
			continue
		}
		apid, apqi, ok := parseAPQNFromOverridePath(p)
		if !ok {
			logphase.Warnf(logphase.Preparation, "cannot parse APQN from %s", p)
			continue
		}
		if _, claimed := live[mdev.APQN{APID: apid, APQI: apqi}]; claimed {
			continue
		}
		logphase.Logf(logphase.Preparation, "draining orphaned vfio-ap queue %s.%s", apid, apqi)
		if err := SwitchToZcrypt(apid, apqi); err != nil {
			logphase.Warnf(logphase.Preparation, "drain %s.%s: %v", apid, apqi, err)
		}
	}
	return nil
}

// liveAPQNs returns the APQNs claimed by existing vfio_ap mdevs. A missing
// matrix root means the module holds no mdevs. Any other listing failure is
// returned, because an empty answer would make every claimed queue look
// orphaned and eligible for draining.
func liveAPQNs() (map[mdev.APQN]struct{}, error) {
	out := make(map[mdev.APQN]struct{})
	entries, err := os.ReadDir(mdev.VFIOAPMatrixPath)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list mdevs under %s: %w", mdev.VFIOAPMatrixPath, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rows, err := mdev.ReadMatrix(e.Name())
		if errors.Is(err, fs.ErrNotExist) {
			// Not a live mdev: either the driver's own attribute
			// subtree, which sits under the matrix root beside the
			// mediated devices (mdev_supported_types) and carries no
			// matrix, or an mdev removed after ReadDir. Neither claims
			// a queue, so both are skipped without a word.
			continue
		}
		if err != nil {
			logphase.Warnf(logphase.Preparation, "read matrix of mdev %s: %v", e.Name(), err)
			continue
		}
		for _, r := range rows {
			out[r] = struct{}{}
		}
	}
	return out, nil
}

// parseAPQNFromOverridePath extracts apid and apqi from a path like
// /sys/bus/ap/devices/card01/01.0002/driver_override.
func parseAPQNFromOverridePath(p string) (apid, apqi string, ok bool) {
	queueDir := filepath.Base(filepath.Dir(p))
	apid, apqi, ok = strings.Cut(queueDir, ".")
	return
}
