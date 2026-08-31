package cryptoconfig

import (
	"os"
	"path/filepath"
	"testing"

	resourceapi "k8s.io/api/resource/v1"
	"sigs.k8s.io/yaml"
)

// shippedDriverName is the driver name the binary registers under, as the
// shipped manifests spell it. Duplicated rather than imported: the constant
// lives in package main, which an internal package cannot reach. A manifest
// that stops matching it trips the vacuous-run guard at the end of the test.
const shippedDriverName = "cex-driver.ibm.com"

// shippedDeviceClasses are the DeviceClass manifests the driver ships, relative
// to this package. A class that carries no CryptoConfig block is valid (the
// driver's built-in defaults cover it), so this list is "every manifest to
// check", not "every manifest that must have one".
var shippedDeviceClasses = []string{
	"../../deploy/kustomize/components/deviceclass-vm/deviceclass-vm.yaml",
	"../../deploy/kustomize/components/feature-container-workload/deviceclass-container.yaml",
}

// TestShippedDeviceClassDefaultsMatchBuiltIn guards the one way restating the
// defaults in a manifest can go wrong: the two copies drifting apart.
//
// The manifest exists so an operator can read the effective configuration off
// the DeviceClass. That only holds while it agrees with the driver, and nothing
// at runtime would notice if it stopped - a stale manifest value silently
// becomes the effective one for every claim that does not override it, which is
// worse than having shipped no manifest config at all.
//
// The comparison is against the whole Defaults() value, not field by field, so
// a field added to the schema and forgotten in the manifest fails here too.
func TestShippedDeviceClassDefaultsMatchBuiltIn(t *testing.T) {
	want := Defaults()
	checked := 0

	for _, path := range shippedDeviceClasses {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path) //nolint:gosec // fixed in-repo manifest path
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			var dc resourceapi.DeviceClass
			if err := yaml.Unmarshal(raw, &dc); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}

			for i, cfg := range dc.Spec.Config {
				if cfg.Opaque == nil {
					continue
				}
				// A DeviceClass may carry config for other drivers.
				if cfg.Opaque.Driver != shippedDriverName {
					continue
				}
				// Parse, don't just compare text: this asserts the manifest is
				// something the driver would actually accept at PREPARE, so a
				// typo'd field name fails here rather than at a claim.
				got, err := Parse(cfg.Opaque.Parameters.Raw)
				if err != nil {
					t.Fatalf("%s config[%d]: driver rejects its own shipped config: %v", path, i, err)
				}
				if *got != want {
					t.Errorf("%s config[%d] = %+v, want %+v (shipped DeviceClass drifted from the driver's built-in defaults)",
						path, i, *got, want)
				}
				checked++
			}
		})
	}

	if checked == 0 {
		t.Error("no shipped DeviceClass carries a CryptoConfig block, so this test would pass vacuously forever")
	}
}
