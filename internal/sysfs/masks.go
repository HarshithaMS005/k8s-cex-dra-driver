package sysfs

import (
	"fmt"
	"strings"
)

const (
	// APMaskPath is the sysfs AP bus attribute masking which adapters
	// zcrypt controls. Clear bits leave adapters to alternate drivers.
	APMaskPath = "/sys/bus/ap/apmask"

	// AQMaskPath is the sysfs AP bus attribute masking which usage
	// domains zcrypt controls. Clear bits leave domains to alternate
	// drivers.
	AQMaskPath = "/sys/bus/ap/aqmask"
)

// ReadCurrentMasks reads the current AP and AQ masks from sysfs.
func ReadCurrentMasks() (apmask, aqmask string, err error) {
	ap, err := ReadFile(APMaskPath)
	if err != nil {
		return "", "", fmt.Errorf("read apmask: %w", err)
	}
	aq, err := ReadFile(AQMaskPath)
	if err != nil {
		return "", "", fmt.Errorf("read aqmask: %w", err)
	}
	return NormalizeMask(ap), NormalizeMask(aq), nil
}

// NormalizeMask strips commas, newlines, and spaces from kernel mask output
// for comparison purposes.
func NormalizeMask(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "\n", "")
	return s
}
