// Package cryptoconfig defines the CryptoConfig opaque device configuration
// schema for the cex DRA driver and the resolution model that merges
// DeviceClass and ResourceClaim config into one effective configuration.
//
// The three-layer resolution implemented here (built-in defaults, then
// FromClass, then FromClaim) is the base model. Per-request scope and
// order-independent merging refine it.
package cryptoconfig

import (
	"bytes"
	"encoding/json"
	"fmt"

	resourceapi "k8s.io/api/resource/v1"
)

// Schema envelope identifiers. Parse rejects any other values.
const (
	Kind    = "CryptoConfig"
	Group   = "cex.ibm.com"
	Version = "v1alpha1"

	APIVersion = Group + "/" + Version
)

// Built-in default field values. These form the base layer of config
// resolution and guarantee correct behavior when no config is provided.
const (
	DefaultControlDomainMode = ControlDomainModeUsageAndControl
)

// CryptoConfig is the opaque device configuration passed to the cex driver via
// DRA OpaqueDeviceConfiguration parameters. Field semantics are defined by the
// consuming feature (e.g. controlDomainMode). This type only defines the
// schema and how layers merge.
type CryptoConfig struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`

	// ControlDomainMode is interpreted by the driver. Empty means "not set at
	// this layer" and retains the value from the previous resolution layer.
	ControlDomainMode string `json:"controlDomainMode,omitempty"`
}

// Defaults returns the built-in default configuration: the base layer of
// resolution.
func Defaults() CryptoConfig {
	return CryptoConfig{
		Kind:              Kind,
		APIVersion:        APIVersion,
		ControlDomainMode: DefaultControlDomainMode,
	}
}

// Parse strictly decodes a raw CryptoConfig from the JSON bytes of an
// OpaqueDeviceConfiguration.Parameters field. Unknown fields, a wrong kind, or
// an unsupported apiVersion are rejected so that typos and version skew fail
// loudly rather than silently misconfigure.
func Parse(raw []byte) (*CryptoConfig, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var cc CryptoConfig
	if err := dec.Decode(&cc); err != nil {
		return nil, fmt.Errorf("decode CryptoConfig: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("decode CryptoConfig: unexpected trailing data")
	}
	if cc.Kind != Kind {
		return nil, fmt.Errorf("unexpected kind %q, want %q", cc.Kind, Kind)
	}
	if cc.APIVersion != APIVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q, want %q", cc.APIVersion, APIVersion)
	}
	return &cc, nil
}

// Resolve applies the three-layer resolution model to the combined
// config array from claim.Status.Allocation.Devices.Config. It starts from the
// built-in defaults and merges the non-empty fields of every entry whose opaque
// driver matches driverName, in array order. The Kubernetes API guarantees
// FromClass entries precede FromClaim entries, so claim config overrides class
// config without explicit sorting.
func Resolve(driverName string, configs []resourceapi.DeviceAllocationConfiguration) (CryptoConfig, error) {
	effective := Defaults()
	for i, c := range configs {
		if c.Opaque == nil || c.Opaque.Driver != driverName {
			continue
		}
		parsed, err := Parse(c.Opaque.Parameters.Raw)
		if err != nil {
			return CryptoConfig{}, fmt.Errorf("config[%d]: %w", i, err)
		}
		effective.merge(parsed)
	}
	return effective, nil
}

// merge overwrites effective's fields with the non-empty fields of o. Each new
// CryptoConfig field adds one clause here.
func (c *CryptoConfig) merge(o *CryptoConfig) {
	if o.ControlDomainMode != "" {
		c.ControlDomainMode = o.ControlDomainMode
	}
}
