package cryptoconfig

import (
	"strings"
	"testing"
)

// mask64 is a well-formed custom control-domain mask: "0x" + 64 hex digits.
const mask64 = "0x" +
	"0000000000000000000000000000000000000000000000000000000000000003"

func TestClassifyControlDomainMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want ControlDomainModeState
	}{
		{"usage-only is enabled", ControlDomainModeUsageOnly, ControlDomainModeEnabled},
		{"usage-and-control is enabled", ControlDomainModeUsageAndControl, ControlDomainModeEnabled},
		{"default is enabled", DefaultControlDomainMode, ControlDomainModeEnabled},
		{"all-control-domains is defined but not enabled", ControlDomainModeAllControlDomains, ControlDomainModeNotEnabled},
		{"custom mask is defined but not enabled", mask64, ControlDomainModeNotEnabled},
		{"uppercase digits are a valid mask", "0x" + strings.ToUpper(mask64[2:]), ControlDomainModeNotEnabled},
		{"63 digits is not a mask", mask64[:len(mask64)-1], ControlDomainModeUnknown},
		{"65 digits is not a mask", mask64 + "0", ControlDomainModeUnknown},
		{"mask without 0x is unknown", mask64[2:], ControlDomainModeUnknown},
		{"non-hex digit in mask is unknown", mask64[:len(mask64)-1] + "g", ControlDomainModeUnknown},
		{"near-miss typo is unknown", "all-control-domain", ControlDomainModeUnknown},
		{"empty is unknown", "", ControlDomainModeUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyControlDomainMode(tc.mode); got != tc.want {
				t.Errorf("ClassifyControlDomainMode(%q) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}

func TestValidateControlDomainMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		// wantErrParts are substrings the message must carry. Empty means the
		// mode is served and no error is expected.
		wantErrParts []string
	}{
		{name: "usage-only serves", mode: ControlDomainModeUsageOnly},
		{name: "usage-and-control serves", mode: ControlDomainModeUsageAndControl},
		{
			name: "all-control-domains names the mode and the enabled set",
			mode: ControlDomainModeAllControlDomains,
			wantErrParts: []string{
				"is not enabled", ControlDomainModeAllControlDomains,
				ControlDomainModeUsageOnly, ControlDomainModeUsageAndControl,
			},
		},
		{
			name:         "custom mask is not enabled, not unknown",
			mode:         mask64,
			wantErrParts: []string{"is not enabled", mask64},
		},
		{
			name:         "typo reads as unknown and lists the defined values",
			mode:         "all-control-domain",
			wantErrParts: []string{"unknown controlDomainMode", "all-control-domain", ControlDomainModeAllControlDomains, "64 hex digits"},
		},
		{
			name:         "malformed mask reads as unknown, not as not-enabled",
			mode:         "0xdeadbeef",
			wantErrParts: []string{"unknown controlDomainMode", "0xdeadbeef"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateControlDomainMode(tc.mode)
			if len(tc.wantErrParts) == 0 {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("err = nil, want an error mentioning %q", tc.wantErrParts)
			}
			for _, part := range tc.wantErrParts {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("err = %q, want it to contain %q", err, part)
				}
			}
		})
	}
}

// TestValidateControlDomainModeDistinguishesFailures pins the property the
// closed value set exists for: a not-enabled mode and a typo must not produce
// the same diagnostic.
func TestValidateControlDomainModeDistinguishesFailures(t *testing.T) {
	notEnabled := ValidateControlDomainMode(ControlDomainModeAllControlDomains)
	unknown := ValidateControlDomainMode("all-control-domain")
	if notEnabled == nil || unknown == nil {
		t.Fatalf("both values must fail: notEnabled=%v unknown=%v", notEnabled, unknown)
	}
	if notEnabled.Error() == unknown.Error() {
		t.Errorf("not-enabled and unknown share a message: %q", notEnabled)
	}
	if strings.Contains(notEnabled.Error(), "unknown") {
		t.Errorf("not-enabled message calls the mode unknown: %q", notEnabled)
	}
}
