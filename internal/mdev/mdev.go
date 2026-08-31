// Package mdev manages vfio-ap mediated devices via the
// /sys/devices/vfio_ap/matrix sysfs interface.
package mdev

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s-cex-dra-driver/internal/logphase"
)

// VFIOAPMatrixPath roots every mdev attribute this package reads or writes.
// It is a var, not a const, so a test can point the package at a fake sysfs
// tree. Nothing in production reassigns it.
var VFIOAPMatrixPath = "/sys/devices/vfio_ap/matrix"

// sysfsWriteMode is the mode passed to os.WriteFile for sysfs
// attributes. The kernel ignores it. 0o600 satisfies gosec G306.
const sysfsWriteMode os.FileMode = 0o600

// mdevCreatePath returns the sysfs attribute that creates a vfio-ap mdev.
// Derived from VFIOAPMatrixPath so overriding that root moves this too.
func mdevCreatePath() string {
	return filepath.Join(VFIOAPMatrixPath, "mdev_supported_types", "vfio_ap-passthrough", "create")
}

// APQN identifies an AP queue by its adapter id and queue index.
type APQN struct {
	APID string
	APQI string
}

// Exists checks if a mediated device with the given UUID exists.
func Exists(uuid string) bool {
	path := filepath.Join(VFIOAPMatrixPath, uuid)
	_, err := os.Stat(path)
	return err == nil
}

// ReadMatrix returns the APQNs assigned to the mdev by reading
// /sys/devices/vfio_ap/matrix/<uuid>/matrix. Each non-empty line is
// "<apid>.<apqi>". This is the authoritative, restart-survivable source
// for which queues a claim has bound to vfio-ap.
func ReadMatrix(uuid string) ([]APQN, error) {
	path := filepath.Join(VFIOAPMatrixPath, uuid, "matrix")
	// G304: path is VFIOAPMatrixPath/<uuid>/matrix. The uuid is either a directory
	// entry inside VFIOAPMatrixPath (recovery scan) or a kubelet-supplied claim
	// UID. Both are constrained to UUID format.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var out []APQN
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		apid, apqi, ok := strings.Cut(line, ".")
		if !ok {
			return nil, fmt.Errorf("invalid matrix line %q for mdev %s", line, uuid)
		}
		out = append(out, APQN{APID: apid, APQI: apqi})
	}
	return out, nil
}

// Create creates a new vfio-ap mediated device.
func Create(uuid string) error {
	logphase.Logf(logphase.Preparation, "Creating mdev: %s", uuid)
	if err := os.WriteFile(mdevCreatePath(), []byte(uuid), sysfsWriteMode); err != nil {
		return fmt.Errorf("create mdev %s: %w", uuid, err)
	}
	logphase.Logf(logphase.Preparation, "Mdev %s created successfully", uuid)
	return nil
}

// Destroy removes a vfio-ap mediated device.
func Destroy(uuid string) error {
	if !Exists(uuid) {
		logphase.Logf(logphase.Preparation, "Mdev %s does not exist (idempotent)", uuid)
		return nil
	}

	logphase.Logf(logphase.Preparation, "Destroying mdev: %s", uuid)
	removePath := filepath.Join(VFIOAPMatrixPath, uuid, "remove")
	if err := os.WriteFile(removePath, []byte("1"), sysfsWriteMode); err != nil {
		return fmt.Errorf("destroy mdev %s: %w", uuid, err)
	}
	logphase.Logf(logphase.Preparation, "Mdev %s destroyed successfully", uuid)
	return nil
}

// AssignAdapter assigns an AP adapter to the mdev.
// adapter is a hex string like "01" or "0a".
func AssignAdapter(uuid string, adapter string) error {
	path := filepath.Join(VFIOAPMatrixPath, uuid, "assign_adapter")
	value := fmt.Sprintf("0x%s", adapter)
	logphase.Logf(logphase.Preparation, "Assigning adapter %s to mdev %s", value, uuid)
	if err := os.WriteFile(path, []byte(value), sysfsWriteMode); err != nil {
		return fmt.Errorf("assign adapter %s to mdev %s: %w", value, uuid, err)
	}
	return nil
}

// AssignDomain assigns an AP domain to the mdev.
// domain is a hex string like "0002" or "002a".
func AssignDomain(uuid string, domain string) error {
	path := filepath.Join(VFIOAPMatrixPath, uuid, "assign_domain")
	value := fmt.Sprintf("0x%s", domain)
	logphase.Logf(logphase.Preparation, "Assigning domain %s to mdev %s", value, uuid)
	if err := os.WriteFile(path, []byte(value), sysfsWriteMode); err != nil {
		return fmt.Errorf("assign domain %s to mdev %s: %w", value, uuid, err)
	}
	return nil
}

// AssignControlDomain assigns an AP control domain to the mdev, setting the
// corresponding bit in its ADM. domain is a hex string like "0002" or "002a".
//
// A control domain grants the guest key-management access to that domain,
// independently of the usage access assign_domain grants. The kernel intersects
// the ADM with the host's ap_control_domain_mask, so this writes the requested
// set and the kernel decides the effective one.
func AssignControlDomain(uuid string, domain string) error {
	path := filepath.Join(VFIOAPMatrixPath, uuid, "assign_control_domain")
	value := fmt.Sprintf("0x%s", domain)
	logphase.Logf(logphase.Preparation, "Assigning control domain %s to mdev %s", value, uuid)
	if err := os.WriteFile(path, []byte(value), sysfsWriteMode); err != nil {
		return fmt.Errorf("assign control domain %s to mdev %s: %w", value, uuid, err)
	}
	return nil
}
