// Package metadata writes the KEP-5304 device metadata files that KubeVirt's
// virt-launcher reads to discover DRA-allocated mediated devices.
package metadata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"k8s-cex-dra-driver/internal/logphase"
)

const (
	metadataSubDir        = "dra-device-metadata"
	metadataContainerBase = "/var/run/kubernetes.io/dra-device-attributes"
	metadataFileName      = "metadata.json"

	// metadataFileMode is the mode for the written metadata file. This driver
	// writes it as root, but virt-launcher reads it as the non-root qemu user
	// (uid 107, KubeVirt's default - root needs the deprecated Root feature
	// gate), so the world-read bit is required. The file holds only claim
	// metadata, no secrets.
	metadataFileMode os.FileMode = 0o644

	// metadataDirMode is the mode for the metadata host directory. Group
	// needs the execute bit to traverse into it. World has no access.
	metadataDirMode os.FileMode = 0o750

	// KubeVirt distinguishes direct claims from template-generated claims by
	// the parent subdir under metadataContainerBase (matching the KEP-5304
	// layout in pkg/dra/utils.go).
	resourceClaimsSubdir         = "resourceclaims"
	resourceClaimTemplatesSubdir = "resourceclaimtemplates"

	metadataKind       = "DeviceMetadata"
	metadataAPIVersion = "metadata.resource.k8s.io/v1alpha1"

	// PodClaimNameAnnotation is the annotation key set by the Kubernetes
	// resource claim controller on claims generated from a template.
	PodClaimNameAnnotation = "resource.kubernetes.io/pod-claim-name"
)

// DeviceMetadata is the KEP-5304 metadata file structure that KubeVirt
// reads to discover DRA-allocated mediated devices.
type DeviceMetadata struct {
	Kind         string           `json:"kind"`
	APIVersion   string           `json:"apiVersion"`
	Metadata     ObjectMeta       `json:"metadata"`
	PodClaimName *string          `json:"podClaimName,omitempty"`
	Requests     []RequestDevices `json:"requests"`
}

// ObjectMeta holds the claim identity fields.
type ObjectMeta struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	UID        string `json:"uid"`
	Generation int64  `json:"generation"`
}

// RequestDevices groups devices allocated for a single request.
type RequestDevices struct {
	Name    string        `json:"name"`
	Devices []DeviceEntry `json:"devices"`
}

// DeviceEntry describes one allocated device with its attributes.
type DeviceEntry struct {
	Driver     string                     `json:"driver"`
	Pool       string                     `json:"pool"`
	Name       string                     `json:"name"`
	Attributes map[string]DeviceAttribute `json:"attributes"`
}

// DeviceAttribute holds a typed attribute value using pointer fields
// to match the Kubernetes DeviceAttribute JSON encoding.
// The Go field names have a Value suffix (matching k8s.io/api/resource/v1),
// but the JSON keys omit it (e.g. "string", not "stringValue").
type DeviceAttribute struct {
	StringValue *string `json:"string,omitempty"`
}

// DeviceInfo carries the allocation result fields needed to build a DeviceEntry.
type DeviceInfo struct {
	Driver string
	Pool   string
	Name   string
}

// ClaimParams groups the inputs for metadata file generation.
type ClaimParams struct {
	PluginDataDir  string
	ClaimName      string
	ClaimNamespace string
	ClaimUID       string
	DriverName     string
	MdevUUID       string
	PodClaimName   string // empty if not a template claim
	RequestDevices map[string][]DeviceInfo
}

// Mount describes one metadata file's host and container paths,
// for embedding as a CDI mount entry.
type Mount struct {
	HostPath      string
	ContainerPath string
}

// GenerateClaimMetadata writes KEP-5304 metadata files for a prepared VM claim.
// One file is written per request at:
//
//	{pluginDataDir}/dra-device-metadata/{claimUID}/{requestName}/metadata.json
//
// Each request produces exactly one device entry regardless of how many AP
// queues were allocated, because all queues in a VM claim share a single mdev.
// KubeVirt requires exactly one device per request.
//
// Returns the mount information for each request, to be embedded as CDI mounts.
func GenerateClaimMetadata(p *ClaimParams) ([]Mount, error) {
	var mounts []Mount
	mdevUUID := p.MdevUUID

	for requestName, devices := range p.RequestDevices {
		if len(devices) == 0 {
			continue
		}
		// Use the first allocated device as the representative entry.
		// All devices in a VM claim share a single mdev, so one entry
		// with the mdevUUID is sufficient for KubeVirt discovery.
		d := devices[0]
		uuid := mdevUUID
		entries := []DeviceEntry{{
			Driver: d.Driver,
			Pool:   d.Pool,
			Name:   d.Name,
			Attributes: map[string]DeviceAttribute{
				"mdevUUID": {StringValue: &uuid},
			},
		}}

		meta := &DeviceMetadata{
			Kind:       metadataKind,
			APIVersion: metadataAPIVersion,
			Metadata: ObjectMeta{
				Name:      p.ClaimName,
				Namespace: p.ClaimNamespace,
				UID:       p.ClaimUID,
				// First write of a claim's lifecycle. Resolved against any
				// existing file below, per the KEP-5304 lifecycle.
				Generation: 1,
			},
			Requests: []RequestDevices{
				{
					Name:    requestName,
					Devices: entries,
				},
			},
		}

		if p.PodClaimName != "" {
			meta.PodClaimName = &p.PodClaimName
		}

		hostDir := filepath.Join(p.PluginDataDir, metadataSubDir, p.ClaimUID, requestName)
		hostPath := filepath.Join(hostDir, metadataFileName)

		// KEP-5304 lifecycle: generation starts at 1 and increments each
		// time an update changes the file for the same prepared claim, so
		// a consumer caching by generation sees every content change - a
		// re-prepare after a driver upgrade may write richer attributes.
		// A byte-identical re-prepare (kubelet retry, driver restart)
		// keeps the previous generation. An unreadable or foreign file is
		// overwritten as a fresh generation 1.
		if prevRaw, err := os.ReadFile(hostPath); err == nil {
			var prev DeviceMetadata
			if json.Unmarshal(prevRaw, &prev) == nil && prev.Metadata.Generation >= 1 {
				meta.Metadata.Generation = prev.Metadata.Generation
				if rendered, err := json.MarshalIndent(meta, "", "  "); err == nil && !bytes.Equal(rendered, prevRaw) {
					meta.Metadata.Generation++
				}
			}
		}

		data, err := json.MarshalIndent(meta, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal metadata for request %s: %w", requestName, err)
		}

		if err := os.MkdirAll(hostDir, metadataDirMode); err != nil {
			return nil, fmt.Errorf("create metadata directory %s: %w", hostDir, err)
		}

		if err := os.WriteFile(hostPath, data, metadataFileMode); err != nil {
			return nil, fmt.Errorf("write metadata file %s: %w", hostPath, err)
		}

		// Pick the subdir + claim ref name to match the layout KubeVirt's
		// virt-launcher expects. Template-generated claims live under
		// resourceclaimtemplates/{podClaimName}. Direct claims live under
		// resourceclaims/{claimName}.
		subdir := resourceClaimsSubdir
		claimRefName := p.ClaimName
		if p.PodClaimName != "" {
			subdir = resourceClaimTemplatesSubdir
			claimRefName = p.PodClaimName
		}
		containerPath := filepath.Join(
			metadataContainerBase, subdir, claimRefName, requestName,
			p.DriverName+"-metadata.json",
		)

		mounts = append(mounts, Mount{
			HostPath:      hostPath,
			ContainerPath: containerPath,
		})

		logphase.Logf(logphase.Preparation, "KEP-5304 metadata written: %s (mdevUUID: %s)", hostPath, p.MdevUUID)
	}

	return mounts, nil
}

// ClaimMetadataExists reports whether claim metadata is on disk. Only a
// VM-path Prepare writes it, so Unprepare consults it - before its own
// delete - as the restart-surviving witness that the claim was a VM claim.
func ClaimMetadataExists(pluginDataDir, claimUID string) bool {
	_, err := os.Stat(filepath.Join(pluginDataDir, metadataSubDir, claimUID))
	return err == nil
}

// GCStaleClaimMetadata removes metadata directories for claims that no
// longer have a live mdev. Metadata is written at Prepare and removed at
// Unprepare, so a claim that dies without an Unprepare leaks its directory
// forever. A missing metadata root is a fresh node, not an error.
// Best-effort per claim. An error is returned only when the listing itself
// fails.
func GCStaleClaimMetadata(pluginDataDir string, live func(claimUID string) bool) error {
	root := filepath.Join(pluginDataDir, metadataSubDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("list metadata directories under %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		claimUID := e.Name()
		if live(claimUID) {
			continue
		}
		claimDir := filepath.Join(root, claimUID)
		if err := os.RemoveAll(claimDir); err != nil {
			logphase.Warnf(logphase.Preparation, "remove stale metadata directory %s: %v", claimDir, err)
			continue
		}
		logphase.Logf(logphase.Preparation, "removed stale KEP-5304 metadata %s (no live mdev for claim %s)", claimDir, claimUID)
	}
	return nil
}

// DeleteClaimMetadata removes all metadata files for a claim.
// Idempotent: returns nil if the claim directory does not exist.
func DeleteClaimMetadata(pluginDataDir, claimUID string) error {
	claimDir := filepath.Join(pluginDataDir, metadataSubDir, claimUID)
	if err := os.RemoveAll(claimDir); err != nil {
		return fmt.Errorf("remove metadata directory %s: %w", claimDir, err)
	}
	logphase.Logf(logphase.Preparation, "KEP-5304 metadata removed: %s", claimDir)
	return nil
}
