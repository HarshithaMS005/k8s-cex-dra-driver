package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"

	"k8s-cex-dra-driver/internal/apscanner"
	"k8s-cex-dra-driver/internal/devicestate"
	"k8s-cex-dra-driver/internal/health"
	"k8s-cex-dra-driver/internal/mdev"
	"k8s-cex-dra-driver/internal/sysfs"
)

const testDriverName = "cex-driver.ibm.com"

// newTestDriver builds a Driver by struct literal, bypassing New: New starts
// the kubelet plugin helper and the scan loop, which need a kubelet socket
// and stay e2e-covered. The helper is left nil on purpose - any code path
// under test that tried to publish would panic instead of passing silently.
// The sysfs roots are pointed at empty temp trees so nothing reads the host.
func newTestDriver(t *testing.T) *Driver {
	t.Helper()

	apRoot := t.TempDir()
	matrix := t.TempDir()
	oldAP, oldMatrix := sysfs.APDevicesPath, mdev.VFIOAPMatrixPath
	sysfs.APDevicesPath, mdev.VFIOAPMatrixPath = apRoot, matrix
	t.Cleanup(func() { sysfs.APDevicesPath, mdev.VFIOAPMatrixPath = oldAP, oldMatrix })

	return &Driver{
		state:       devicestate.New("node", testDriverName, t.TempDir(), t.TempDir()),
		machineID:   "machine",
		mkvpTracker: apscanner.NewMKVPTracker(),
		scanTrigger: make(chan struct{}, 1),
	}
}

func TestTriggerScanCoalesces(t *testing.T) {
	d := newTestDriver(t)

	d.triggerScan()
	d.triggerScan()
	if got := len(d.scanTrigger); got != 1 {
		t.Fatalf("pending scan requests = %d, want 1 (second trigger must coalesce)", got)
	}

	<-d.scanTrigger
	d.triggerScan()
	if got := len(d.scanTrigger); got != 1 {
		t.Fatalf("pending scan requests after drain = %d, want 1", got)
	}
}

// TestScanAndPublishSkipsUnchangedScan pins that a quiet scan cycle never
// reaches the publisher: d.helper is nil, so any publish attempt panics.
func TestScanAndPublishSkipsUnchangedScan(t *testing.T) {
	d := newTestDriver(t)

	devices, err := apscanner.Scan(d.machineID, d.mkvpTracker)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	d.state.CompareAndUpdate(devices)

	if err := d.scanAndPublish(t.Context()); err != nil {
		t.Fatalf("scanAndPublish with an unchanged tree: %v", err)
	}
}

func TestPrepareResourceClaimRejectsUnallocatedClaim(t *testing.T) {
	d := newTestDriver(t)
	claim := &resourceapi.ResourceClaim{}
	claim.UID = "claim-unallocated"

	result := d.prepareResourceClaim(t.Context(), claim)
	if result.Err == nil {
		t.Fatal("prepareResourceClaim accepted a claim without an allocation")
	}
	for _, want := range []string{"claim-unallocated", "not yet allocated"} {
		if !strings.Contains(result.Err.Error(), want) {
			t.Errorf("Err = %q, want it to contain %q", result.Err, want)
		}
	}
}

// TestPrepareResourceClaimWrapsStateError pins the RPC error contract: a
// devicestate rejection comes back inside PrepareResult naming the claim,
// not as an RPC-level error that would fail the whole batch.
func TestPrepareResourceClaimWrapsStateError(t *testing.T) {
	d := newTestDriver(t)
	claim := &resourceapi.ResourceClaim{
		Spec: resourceapi.ResourceClaimSpec{
			Devices: resourceapi.DeviceClaim{
				Requests: []resourceapi.DeviceRequest{{
					Name:    "ap-queue",
					Exactly: &resourceapi.ExactDeviceRequest{DeviceClassName: devicestate.DeviceClassContainer},
				}},
			},
		},
		Status: resourceapi.ResourceClaimStatus{
			Allocation: &resourceapi.AllocationResult{
				Devices: resourceapi.DeviceAllocationResult{
					Results: []resourceapi.DeviceRequestAllocationResult{{
						Request: "ap-queue",
						Driver:  testDriverName,
						Pool:    "card01",
						Device:  "cex-01-0002",
					}},
				},
			},
		},
	}
	claim.UID = "claim-container"

	result := d.prepareResourceClaim(t.Context(), claim)
	if result.Err == nil {
		t.Fatal("prepareResourceClaim accepted a container claim with the gate disabled")
	}
	for _, want := range []string{"claim-container", devicestate.DeviceClassContainer} {
		if !strings.Contains(result.Err.Error(), want) {
			t.Errorf("Err = %q, want it to contain %q", result.Err, want)
		}
	}
	if len(result.Devices) != 0 {
		t.Errorf("Devices = %v, want none on error", result.Devices)
	}
}

// TestUnprepareResourceClaimUnknownClaim covers the kubelet's replayed
// unprepare after a restart: no mdev, no metadata, no CDI spec on disk. The
// wrapper must report success so the kubelet stops retrying.
func TestUnprepareResourceClaimUnknownClaim(t *testing.T) {
	d := newTestDriver(t)
	claim := kubeletplugin.NamespacedObject{}
	claim.UID = "claim-gone"
	claim.Name = "claim"
	claim.Namespace = "ns"

	if err := d.unprepareResourceClaim(t.Context(), claim); err != nil {
		t.Fatalf("unprepareResourceClaim for an unknown claim: %v", err)
	}
}

// TestWatchHealthStatusReportsUnknown pins the interim health contract: one
// snapshot covering every published device at Unknown, sent on a stream that
// stays open. Declining instead closes the stream, and a kubelet 1.36 redials
// a closed stream every five seconds while the helper logs each close as an
// error.
func TestWatchHealthStatusReportsUnknown(t *testing.T) {
	d := newTestDriver(t)
	d.state.CompareAndUpdate(map[string][]resourceapi.Device{
		"card01": {{Name: "machine-01-0002"}, {Name: "machine-01-0003"}},
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reports := make(chan kubeletplugin.DeviceHealthReport)
	errCh := make(chan error, 1)
	go func() { errCh <- d.WatchHealthStatus(ctx, reports) }()

	report := <-reports
	if len(report.Devices) != 2 {
		t.Fatalf("reported devices = %d, want 2", len(report.Devices))
	}
	for _, device := range report.Devices {
		if device.Health != kubeletplugin.HealthStatusUnknown {
			t.Errorf("%s health = %q, want %q", device.DeviceName, device.Health, kubeletplugin.HealthStatusUnknown)
		}
		if device.PoolName != "node-card01" {
			t.Errorf("%s pool = %q, want %q", device.DeviceName, device.PoolName, "node-card01")
		}
	}

	// A cancelled stream ends the loop without an error: the kubelet went
	// away, which is not a driver failure.
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("WatchHealthStatus after cancel = %v, want nil", err)
	}
}

// TestRunScanCycleStampsHeartbeatOnError pins what liveness measures: the loop
// turning, not the hardware answering. A cycle that returns an error still
// completed, so it stamps; only a cycle that never returns should fail the
// probe, and that one cannot stamp by construction.
func TestRunScanCycleStampsHeartbeatOnError(t *testing.T) {
	d := newTestDriver(t)
	d.heartbeat = health.NewHeartbeat()

	// A regular file where the AP root belongs makes the scan fail with a
	// non-ENOENT error, which aborts the cycle.
	broken := filepath.Join(t.TempDir(), "ap-root-is-a-file")
	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", broken, err)
	}
	sysfs.APDevicesPath = broken

	if err := d.scanAndPublish(t.Context()); err == nil {
		t.Fatal("scanAndPublish with a broken AP root returned no error")
	}

	time.Sleep(10 * time.Millisecond)
	before := d.heartbeat.Age()
	d.runScanCycle(t.Context())
	if after := d.heartbeat.Age(); after >= before {
		t.Errorf("heartbeat age after a failed cycle = %s, want fresher than %s", after, before)
	}
}
