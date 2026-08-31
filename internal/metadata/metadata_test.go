package metadata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateClaimMetadata_SingleRequest(t *testing.T) {
	pluginDataDir := t.TempDir()
	claimName := "my-cex-claim"
	claimUID := "abc-123-def"
	driverName := "cex-driver.ibm.com"
	requestDevices := map[string][]DeviceInfo{
		"cex-request": {
			{Driver: driverName, Pool: "node-01", Name: "dev-01-0002"},
		},
	}

	mounts, err := GenerateClaimMetadata(&ClaimParams{
		PluginDataDir:  pluginDataDir,
		ClaimName:      claimName,
		ClaimNamespace: "default",
		ClaimUID:       claimUID,
		DriverName:     driverName,
		MdevUUID:       claimUID,
		RequestDevices: requestDevices,
	})
	if err != nil {
		t.Fatalf("GenerateClaimMetadata: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("len(mounts) = %d, want 1", len(mounts))
	}

	data, err := os.ReadFile(mounts[0].HostPath)
	if err != nil {
		t.Fatalf("read metadata file: %v", err)
	}
	var meta DeviceMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if len(meta.Requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(meta.Requests))
	}
	if len(meta.Requests[0].Devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(meta.Requests[0].Devices))
	}

	dev := meta.Requests[0].Devices[0]
	// direct claim → resourceclaims/{claimName}/...
	wantContainerPath := filepath.Join("/var/run/kubernetes.io/dra-device-attributes",
		"resourceclaims", claimName, "cex-request", driverName+"-metadata.json")
	stringChecks := []struct{ name, got, want string }{
		{"hostPath", mounts[0].HostPath, filepath.Join(pluginDataDir, "dra-device-metadata", claimUID, "cex-request", "metadata.json")},
		{"containerPath", mounts[0].ContainerPath, wantContainerPath},
		{"kind", meta.Kind, "DeviceMetadata"},
		{"apiVersion", meta.APIVersion, "metadata.resource.k8s.io/v1alpha1"},
		{"metadata.name", meta.Metadata.Name, claimName},
		{"metadata.namespace", meta.Metadata.Namespace, "default"},
		{"metadata.uid", meta.Metadata.UID, claimUID},
		{"request.name", meta.Requests[0].Name, "cex-request"},
		{"device.driver", dev.Driver, driverName},
		{"device.pool", dev.Pool, "node-01"},
	}
	for _, c := range stringChecks {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
			}
		})
	}
	t.Run("metadata.generation", func(t *testing.T) {
		if meta.Metadata.Generation != 1 {
			t.Errorf("metadata.generation = %d, want 1", meta.Metadata.Generation)
		}
	})
	t.Run("podClaimName", func(t *testing.T) {
		if meta.PodClaimName != nil {
			t.Errorf("podClaimName = %q, want nil", *meta.PodClaimName)
		}
	})
	t.Run("mdevUUID", func(t *testing.T) {
		attr := dev.Attributes["mdevUUID"].StringValue
		if attr == nil {
			t.Errorf("mdevUUID = nil, want %q", claimUID)
		} else if *attr != claimUID {
			t.Errorf("mdevUUID = %q, want %q", *attr, claimUID)
		}
	})
}

func TestGenerateClaimMetadata_PodClaimName(t *testing.T) {
	pluginDataDir := t.TempDir()
	driverName := "cex-driver.ibm.com"

	mounts, err := GenerateClaimMetadata(&ClaimParams{
		PluginDataDir:  pluginDataDir,
		ClaimName:      "generated-claim",
		ClaimNamespace: "ns",
		ClaimUID:       "uid-1",
		DriverName:     driverName,
		MdevUUID:       "uid-1",
		PodClaimName:   "my-template-ref",
		RequestDevices: map[string][]DeviceInfo{
			"req": {{Driver: driverName, Pool: "p", Name: "d"}},
		},
	})
	if err != nil {
		t.Fatalf("GenerateClaimMetadata: %v", err)
	}

	data, err := os.ReadFile(mounts[0].HostPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var meta DeviceMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if meta.PodClaimName == nil || *meta.PodClaimName != "my-template-ref" {
		t.Errorf("podClaimName = %v, want %q", meta.PodClaimName, "my-template-ref")
	}

	// Template claim → resourceclaimtemplates/{podClaimName}/...
	wantContainerPath := filepath.Join(
		"/var/run/kubernetes.io/dra-device-attributes",
		"resourceclaimtemplates", "my-template-ref", "req",
		driverName+"-metadata.json",
	)
	if mounts[0].ContainerPath != wantContainerPath {
		t.Errorf("containerPath = %q, want %q", mounts[0].ContainerPath, wantContainerPath)
	}
}

func TestGenerateClaimMetadata_MultipleRequests(t *testing.T) {
	pluginDataDir := t.TempDir()
	claimUID := "uuid-456"
	driverName := "cex-driver.ibm.com"
	requestDevices := map[string][]DeviceInfo{
		"req-a": {
			{Driver: driverName, Pool: "pool-1", Name: "dev-a1"},
			{Driver: driverName, Pool: "pool-1", Name: "dev-a2"},
		},
		"req-b": {
			{Driver: driverName, Pool: "pool-2", Name: "dev-b1"},
		},
	}

	mounts, err := GenerateClaimMetadata(&ClaimParams{
		PluginDataDir:  pluginDataDir,
		ClaimName:      "multi-claim",
		ClaimNamespace: "default",
		ClaimUID:       claimUID,
		DriverName:     driverName,
		MdevUUID:       claimUID,
		RequestDevices: requestDevices,
	})
	if err != nil {
		t.Fatalf("GenerateClaimMetadata: %v", err)
	}

	if len(mounts) != len(requestDevices) {
		t.Fatalf("len(mounts) = %d, want %d", len(mounts), len(requestDevices))
	}

	for _, m := range mounts {
		data, err := os.ReadFile(m.HostPath)
		if err != nil {
			t.Errorf("read metadata file %s: %v", m.HostPath, err)
			continue
		}

		var meta DeviceMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			t.Errorf("unmarshal metadata %s: %v", m.HostPath, err)
			continue
		}

		if len(meta.Requests) != 1 {
			t.Errorf("file %s: len(requests) = %d, want 1", m.HostPath, len(meta.Requests))
			continue
		}
		// Each request must have exactly 1 device entry (the representative
		// device for the shared mdev), regardless of how many AP queues
		// were allocated. KubeVirt requires exactly one device per request.
		if len(meta.Requests[0].Devices) != 1 {
			t.Errorf("request %s: len(devices) = %d, want 1", meta.Requests[0].Name, len(meta.Requests[0].Devices))
		}
		// The representative device should be the first allocated device
		reqName := meta.Requests[0].Name
		wantName := requestDevices[reqName][0].Name
		if meta.Requests[0].Devices[0].Name != wantName {
			t.Errorf("request %s: device name = %q, want %q", reqName, meta.Requests[0].Devices[0].Name, wantName)
		}
		// mdevUUID must be present
		attr := meta.Requests[0].Devices[0].Attributes["mdevUUID"]
		if attr.StringValue == nil {
			t.Errorf("request %s: mdevUUID = nil, want %q", reqName, claimUID)
		} else if *attr.StringValue != claimUID {
			t.Errorf("request %s: mdevUUID = %q, want %q", reqName, *attr.StringValue, claimUID)
		}
	}
}

func TestGenerateClaimMetadata_OverwriteExisting(t *testing.T) {
	pluginDataDir := t.TempDir()
	driverName := "cex-driver.ibm.com"
	claimUID := "overwrite-uid"
	requestDevices := map[string][]DeviceInfo{
		"req": {{Driver: driverName, Pool: "p", Name: "d"}},
	}

	params := &ClaimParams{
		PluginDataDir:  pluginDataDir,
		ClaimName:      "overwrite-claim",
		ClaimNamespace: "default",
		ClaimUID:       claimUID,
		DriverName:     driverName,
		MdevUUID:       "uuid-1",
		RequestDevices: requestDevices,
	}

	if _, err := GenerateClaimMetadata(params); err != nil {
		t.Fatalf("first write: %v", err)
	}

	params.MdevUUID = "uuid-2"
	mounts, err := GenerateClaimMetadata(params)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	data, err := os.ReadFile(mounts[0].HostPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var meta DeviceMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := meta.Requests[0].Devices[0].Attributes["mdevUUID"].StringValue
	if got == nil {
		t.Errorf("mdevUUID = nil, want %q (overwrite failed)", "uuid-2")
	} else if *got != "uuid-2" {
		t.Errorf("mdevUUID = %q, want %q (overwrite failed)", *got, "uuid-2")
	}

	// The overwrite changed the content, so the KEP-5304 generation must
	// advance for a consumer caching by generation to see the change.
	if meta.Metadata.Generation != 2 {
		t.Errorf("metadata.generation = %d, want 2 after a content change", meta.Metadata.Generation)
	}
}

func TestGenerateClaimMetadata_IdenticalRewriteKeepsGeneration(t *testing.T) {
	pluginDataDir := t.TempDir()
	driverName := "cex-driver.ibm.com"
	params := &ClaimParams{
		PluginDataDir:  pluginDataDir,
		ClaimName:      "retry-claim",
		ClaimNamespace: "default",
		ClaimUID:       "retry-uid",
		DriverName:     driverName,
		MdevUUID:       "retry-uid",
		RequestDevices: map[string][]DeviceInfo{
			"req": {{Driver: driverName, Pool: "p", Name: "d"}},
		},
	}

	if _, err := GenerateClaimMetadata(params); err != nil {
		t.Fatalf("first write: %v", err)
	}
	mounts, err := GenerateClaimMetadata(params)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	data, err := os.ReadFile(mounts[0].HostPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var meta DeviceMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// A byte-identical re-prepare (kubelet retry, driver restart) is not
	// an update, so the generation must not move.
	if meta.Metadata.Generation != 1 {
		t.Errorf("metadata.generation = %d, want 1 after an identical rewrite", meta.Metadata.Generation)
	}
}

func TestDeleteClaimMetadata_Exists(t *testing.T) {
	pluginDataDir := t.TempDir()
	claimUID := "delete-uid"
	driverName := "cex-driver.ibm.com"

	if _, err := GenerateClaimMetadata(&ClaimParams{
		PluginDataDir:  pluginDataDir,
		ClaimName:      "delete-me",
		ClaimNamespace: "default",
		ClaimUID:       claimUID,
		DriverName:     driverName,
		MdevUUID:       claimUID,
		RequestDevices: map[string][]DeviceInfo{
			"req": {{Driver: driverName, Pool: "p", Name: "d"}},
		},
	}); err != nil {
		t.Fatalf("generate: %v", err)
	}

	if err := DeleteClaimMetadata(pluginDataDir, claimUID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	claimDir := filepath.Join(pluginDataDir, "dra-device-metadata", claimUID)
	if _, err := os.Stat(claimDir); !os.IsNotExist(err) {
		t.Errorf("claim directory still exists after deletion")
	}
}

func TestDeleteClaimMetadata_NotExists(t *testing.T) {
	pluginDataDir := t.TempDir()

	if err := DeleteClaimMetadata(pluginDataDir, "nonexistent"); err != nil {
		t.Errorf("delete nonexistent: %v (expected nil)", err)
	}
}

func TestGCStaleClaimMetadata(t *testing.T) {
	pluginDataDir := t.TempDir()
	driverName := "cex-driver.ibm.com"
	for _, claimUID := range []string{"live-claim", "dead-claim"} {
		if _, err := GenerateClaimMetadata(&ClaimParams{
			PluginDataDir:  pluginDataDir,
			ClaimName:      claimUID,
			ClaimNamespace: "default",
			ClaimUID:       claimUID,
			DriverName:     driverName,
			MdevUUID:       claimUID,
			RequestDevices: map[string][]DeviceInfo{
				"req": {{Driver: driverName, Pool: "p", Name: "d"}},
			},
		}); err != nil {
			t.Fatalf("generate %s: %v", claimUID, err)
		}
	}

	live := func(claimUID string) bool { return claimUID == "live-claim" }
	if err := GCStaleClaimMetadata(pluginDataDir, live); err != nil {
		t.Fatalf("GCStaleClaimMetadata: %v", err)
	}

	if ClaimMetadataExists(pluginDataDir, "dead-claim") {
		t.Error("dead-claim metadata still present")
	}
	if !ClaimMetadataExists(pluginDataDir, "live-claim") {
		t.Error("live-claim metadata removed")
	}
}

func TestGCStaleClaimMetadataMissingRoot(t *testing.T) {
	if err := GCStaleClaimMetadata(t.TempDir(), func(string) bool { return false }); err != nil {
		t.Fatalf("GCStaleClaimMetadata on fresh node: %v", err)
	}
}
