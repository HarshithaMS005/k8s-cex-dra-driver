package cryptoconfig

import (
	"fmt"
	"regexp"
)

// The closed set of controlDomainMode values. Naming follows the SE/HMC
// vocabulary of usage and control, ordered as a progression: usage is the
// baseline every mode grants, control is what is added. A fourth value, a
// custom 256-bit hex mask, has no fixed spelling and is recognized by
// customMaskPattern instead.
const (
	ControlDomainModeUsageOnly         = "usage-only"
	ControlDomainModeUsageAndControl   = "usage-and-control"
	ControlDomainModeAllControlDomains = "all-control-domains"
)

// customMaskPattern matches the custom control-domain mask: "0x" followed by
// exactly 64 hex digits, one bit per domain index 0-255.
var customMaskPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)

// ControlDomainModeState is the outcome of classifying a controlDomainMode
// value. The three states drive three different diagnostics, so a claim that
// asks for a mode the driver cannot serve yet is never confused with a typo.
type ControlDomainModeState int

const (
	// ControlDomainModeUnknown is not a defined value: a typo, or a
	// malformed hex mask.
	ControlDomainModeUnknown ControlDomainModeState = iota
	// ControlDomainModeEnabled is a defined value the driver serves. Every
	// enabled mode is bounded by the claim's own allocation.
	ControlDomainModeEnabled
	// ControlDomainModeNotEnabled is a defined value the driver refuses to
	// serve. Every such mode grants control-domain access irrespective of the
	// allocation, which is only safe behind an admission-time policy that does
	// not exist yet.
	ControlDomainModeNotEnabled
)

// ClassifyControlDomainMode sorts a controlDomainMode value into the three
// states. The value set is closed and fixed: enabling a not-enabled mode later
// changes only this function's verdict, never the set of accepted spellings, so
// a claim written against a not-enabled mode keeps its shape.
func ClassifyControlDomainMode(mode string) ControlDomainModeState {
	switch mode {
	case ControlDomainModeUsageOnly, ControlDomainModeUsageAndControl:
		return ControlDomainModeEnabled
	case ControlDomainModeAllControlDomains:
		return ControlDomainModeNotEnabled
	}
	if customMaskPattern.MatchString(mode) {
		return ControlDomainModeNotEnabled
	}
	return ControlDomainModeUnknown
}

// ValidateControlDomainMode returns nil for a mode the driver serves, and
// otherwise an error whose wording tells the two failures apart: a defined mode
// that is not enabled yet calls for waiting or picking a bounded mode, a typo
// calls for a correction.
func ValidateControlDomainMode(mode string) error {
	switch ClassifyControlDomainMode(mode) {
	case ControlDomainModeEnabled:
		return nil
	case ControlDomainModeNotEnabled:
		return fmt.Errorf(
			"controlDomainMode %q is a known mode that is not enabled: it grants "+
				"control-domain access beyond this claim's allocation, which requires "+
				"an admission-time namespace policy the cluster does not have yet. "+
				"Enabled modes are %q and %q",
			mode, ControlDomainModeUsageOnly, ControlDomainModeUsageAndControl)
	default:
		return fmt.Errorf(
			"unknown controlDomainMode %q: defined values are %q, %q, %q, or a "+
				"256-bit hex mask (\"0x\" followed by 64 hex digits)",
			mode, ControlDomainModeUsageOnly, ControlDomainModeUsageAndControl,
			ControlDomainModeAllControlDomains)
	}
}
