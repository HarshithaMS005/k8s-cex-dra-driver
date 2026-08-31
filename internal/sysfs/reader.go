// Package sysfs provides read access to the s390 sysfs and /proc attributes
// used to discover crypto queues, their master-key verification patterns, and
// the machine identifier.
package sysfs

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// APDevicesPath roots the AP device tree the scanner walks and the binding
// package writes to. A var, not a const, so a test can point the packages at a
// fake sysfs tree. Nothing in production reassigns it.
var APDevicesPath = "/sys/devices/ap"

// ReadFile reads a sysfs file and returns its content as a string.
func ReadFile(path string) (string, error) {
	// G304: callers in this module pass paths rooted at APDevicesPath -
	// built segment by segment (apscanner, binding) or matched by
	// filepath.Glob against a fixed pattern (binding/recovery). APDevicesPath
	// is a package var only so tests can redirect it. No user-controlled
	// segment reaches here.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// MachineID reads the sysinfo file at path and constructs a machine
// identifier from the Manufacturer, Type, Plant, and Sequence Code fields. Per
// the PoP (SA22-7832-14, SYSIB 1.1.1), these four fields together identify a
// machine worldwide-uniquely. The result is lowercased for Kubernetes DNS label
// compliance. Example output: "ibm-3931-02-00000000000a8f67".
//
// path comes from --sysinfo-path, which defaults to the container's own
// /proc/sysinfo: a global proc node carrying the same values as the host's, so
// no host mount is involved.
func MachineID(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-configured (--sysinfo-path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return machineIDFromSysinfo(path, string(data))
}

// machineIDFromSysinfo constructs the machine identifier from sysinfo content
// read from path (path is used only for error messages).
func machineIDFromSysinfo(path, content string) (string, error) {
	manufacturer := parseSysinfoField(content, `Manufacturer:\s+(\S+)`)
	machineType := parseSysinfoField(content, `Type:\s+(\S+)`)
	plant := parseSysinfoField(content, `Plant:\s+(\S+)`)
	seqCode := parseSysinfoField(content, `Sequence Code:\s+(\S+)`)

	if manufacturer == "" || machineType == "" || plant == "" || seqCode == "" {
		return "", fmt.Errorf("could not parse machine identity from %s (manufacturer=%q, type=%q, plant=%q, sequence=%q)",
			path, manufacturer, machineType, plant, seqCode)
	}

	return strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", manufacturer, machineType, plant, seqCode)), nil
}

// parseSysinfoField extracts the first capture group matching pattern from content.
func parseSysinfoField(content, pattern string) string {
	re := regexp.MustCompile(pattern)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}
