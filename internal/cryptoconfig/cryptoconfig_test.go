package cryptoconfig

import (
	"strings"
	"testing"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const testDriver = "cex-driver.ibm.com"

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.ControlDomainMode != DefaultControlDomainMode {
		t.Errorf("ControlDomainMode = %q, want %q", d.ControlDomainMode, DefaultControlDomainMode)
	}
	if d.Kind != Kind || d.APIVersion != APIVersion {
		t.Errorf("envelope = %q/%q, want %q/%q", d.Kind, d.APIVersion, Kind, APIVersion)
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantCDM string
		wantErr string
	}{
		{
			name:    "full",
			raw:     `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1","controlDomainMode":"usage-only"}`,
			wantCDM: "usage-only",
		},
		{
			name:    "minimal omits optional field",
			raw:     `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1"}`,
			wantCDM: "",
		},
		{
			name:    "unknown field rejected",
			raw:     `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1","bogus":true}`,
			wantErr: "decode CryptoConfig",
		},
		{
			name:    "wrong kind",
			raw:     `{"kind":"NotCrypto","apiVersion":"cex.ibm.com/v1alpha1"}`,
			wantErr: "unexpected kind",
		},
		{
			name:    "unsupported apiVersion",
			raw:     `{"kind":"CryptoConfig","apiVersion":"v2"}`,
			wantErr: "unsupported apiVersion",
		},
		{
			name:    "bare version without group rejected",
			raw:     `{"kind":"CryptoConfig","apiVersion":"v1alpha1"}`,
			wantErr: "unsupported apiVersion",
		},
		{
			name:    "foreign group rejected",
			raw:     `{"kind":"CryptoConfig","apiVersion":"other.ibm.com/v1alpha1"}`,
			wantErr: "unsupported apiVersion",
		},
		{
			name:    "unknown version under group rejected",
			raw:     `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha2"}`,
			wantErr: "unsupported apiVersion",
		},
		{
			name:    "malformed json",
			raw:     `{"kind":`,
			wantErr: "decode CryptoConfig",
		},
		{
			name:    "trailing data",
			raw:     `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1"}{}`,
			wantErr: "trailing data",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cc, err := Parse([]byte(tc.raw))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if cc.ControlDomainMode != tc.wantCDM {
				t.Errorf("ControlDomainMode = %q, want %q", cc.ControlDomainMode, tc.wantCDM)
			}
		})
	}
}

// cfg builds a DeviceAllocationConfiguration with an opaque payload for driver.
func cfg(driver, source, raw string) resourceapi.DeviceAllocationConfiguration {
	return resourceapi.DeviceAllocationConfiguration{
		Source: resourceapi.AllocationConfigSource(source),
		DeviceConfiguration: resourceapi.DeviceConfiguration{
			Opaque: &resourceapi.OpaqueDeviceConfiguration{
				Driver:     driver,
				Parameters: runtime.RawExtension{Raw: []byte(raw)},
			},
		},
	}
}

func crypto(cdm string) string {
	return `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1","controlDomainMode":"` + cdm + `"}`
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		configs []resourceapi.DeviceAllocationConfiguration
		wantCDM string
		wantErr string
	}{
		{
			name:    "no config yields built-in default",
			configs: nil,
			wantCDM: DefaultControlDomainMode,
		},
		{
			name:    "class only",
			configs: []resourceapi.DeviceAllocationConfiguration{cfg(testDriver, "FromClass", crypto("usage-only"))},
			wantCDM: "usage-only",
		},
		{
			name: "claim overrides class",
			configs: []resourceapi.DeviceAllocationConfiguration{
				cfg(testDriver, "FromClass", crypto("usage-and-control")),
				cfg(testDriver, "FromClaim", crypto("all-control-domains")),
			},
			wantCDM: "all-control-domains",
		},
		{
			name: "other driver ignored",
			configs: []resourceapi.DeviceAllocationConfiguration{
				cfg("other.example.com", "FromClaim", `{"kind":"Whatever"}`),
				cfg(testDriver, "FromClass", crypto("usage-only")),
			},
			wantCDM: "usage-only",
		},
		{
			name: "nil opaque skipped",
			configs: []resourceapi.DeviceAllocationConfiguration{
				{Source: "FromClaim"},
				cfg(testDriver, "FromClass", crypto("usage-only")),
			},
			wantCDM: "usage-only",
		},
		{
			name: "empty field retains previous layer",
			configs: []resourceapi.DeviceAllocationConfiguration{
				cfg(testDriver, "FromClass", crypto("usage-only")),
				cfg(testDriver, "FromClaim", `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1"}`),
			},
			wantCDM: "usage-only",
		},
		{
			name: "later same-driver entry wins",
			configs: []resourceapi.DeviceAllocationConfiguration{
				cfg(testDriver, "FromClaim", crypto("usage-only")),
				cfg(testDriver, "FromClaim", crypto("all-control-domains")),
			},
			wantCDM: "all-control-domains",
		},
		{
			name:    "parse error propagates",
			configs: []resourceapi.DeviceAllocationConfiguration{cfg(testDriver, "FromClaim", `{"kind":"CryptoConfig","apiVersion":"cex.ibm.com/v1alpha1","bogus":1}`)},
			wantErr: "config[0]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eff, err := Resolve(testDriver, tc.configs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if eff.ControlDomainMode != tc.wantCDM {
				t.Errorf("ControlDomainMode = %q, want %q", eff.ControlDomainMode, tc.wantCDM)
			}
		})
	}
}
