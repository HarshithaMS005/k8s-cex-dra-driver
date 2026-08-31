package binding

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/klog/v2"

	"k8s-cex-dra-driver/internal/mdev"
	"k8s-cex-dra-driver/internal/sysfs"
)

// fakeSysfs builds the slice of sysfs this package touches under a temp dir and
// points the package roots at it: one directory per AP queue holding a
// driver_override and a driver/unbind, plus a bus-wide drivers_probe and an
// empty vfio-ap matrix root. Returns the temp dir.
//
// Real sysfs reaches the queues twice over - /sys/bus/ap/devices/cardXX is a
// symlink to /sys/devices/ap/cardXX - and the package uses both views, so both
// roots are aimed at the same tree here.
func fakeSysfs(t *testing.T, overrides map[string]string) string {
	t.Helper()
	root := t.TempDir()

	for queue, override := range overrides {
		apid, apqi, ok := splitQueue(queue)
		if !ok {
			t.Fatalf("malformed queue id %q in test setup", queue)
		}
		dir := filepath.Join(root, "card"+apid, apid+"."+apqi)
		if err := os.MkdirAll(filepath.Join(dir, "driver"), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		writeFile(t, filepath.Join(dir, "driver_override"), override)
		writeFile(t, filepath.Join(dir, "driver", "unbind"), "")
	}

	matrix := filepath.Join(root, "matrix")
	if err := os.MkdirAll(matrix, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", matrix, err)
	}
	writeFile(t, filepath.Join(root, "drivers_probe"), "")

	t.Cleanup(swap(&sysfs.APDevicesPath, root))
	t.Cleanup(swap(&DriversProbePath, filepath.Join(root, "drivers_probe")))
	t.Cleanup(swap(&mdev.VFIOAPMatrixPath, matrix))

	return root
}

// swap sets *p to v and returns a func restoring the old value, for t.Cleanup.
func swap(p *string, v string) func() {
	old := *p
	*p = v
	return func() { *p = old }
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func splitQueue(q string) (apid, apqi string, ok bool) {
	for i := range q {
		if q[i] == '.' {
			return q[:i], q[i+1:], true
		}
	}
	return "", "", false
}

// addMdev creates a live mdev whose matrix claims the given queues.
func addMdev(t *testing.T, uuid string, queues ...string) {
	t.Helper()
	dir := filepath.Join(mdev.VFIOAPMatrixPath, uuid)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	var matrix string
	for _, q := range queues {
		matrix += q + "\n"
	}
	writeFile(t, filepath.Join(dir, "matrix"), matrix)
}

// readOverride returns a queue's driver_override, trimmed: SwitchToZcrypt
// clears the attribute by writing a newline, which the kernel reads as empty.
func readOverride(t *testing.T, root, queue string) string {
	t.Helper()
	apid, apqi, _ := splitQueue(queue)
	got, err := sysfs.ReadFile(filepath.Join(root, "card"+apid, apid+"."+apqi, "driver_override"))
	if err != nil {
		t.Fatalf("read driver_override for %s: %v", queue, err)
	}
	return strings.TrimSpace(got)
}

func TestDrainOrphanedVFIOAP(t *testing.T) {
	tests := []struct {
		name string
		// overrides is the starting driver_override of each queue.
		overrides map[string]string
		// live maps an mdev uuid to the queues its matrix claims.
		live map[string][]string
		// wantDrained lists the queues that must come back to zcrypt, i.e.
		// whose driver_override must be cleared.
		wantDrained []string
		// wantKept lists queues whose driver_override must be untouched.
		wantKept []string
	}{
		{
			name:        "orphan is drained",
			overrides:   map[string]string{"01.0002": "vfio_ap"},
			wantDrained: []string{"01.0002"},
		},
		{
			name:      "queue backed by a live mdev is spared",
			overrides: map[string]string{"01.0002": "vfio_ap"},
			live:      map[string][]string{"uuid-a": {"01.0002"}},
			wantKept:  []string{"01.0002"},
		},
		{
			name:      "zcrypt queue is left alone",
			overrides: map[string]string{"01.0002": ""},
			wantKept:  []string{"01.0002"},
		},
		{
			name: "only the unclaimed queue is drained",
			overrides: map[string]string{
				"01.0002": "vfio_ap",
				"02.0002": "vfio_ap",
				"03.0002": "",
			},
			live:        map[string][]string{"uuid-a": {"02.0002"}},
			wantDrained: []string{"01.0002"},
			wantKept:    []string{"02.0002", "03.0002"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := fakeSysfs(t, tc.overrides)
			for uuid, queues := range tc.live {
				addMdev(t, uuid, queues...)
			}

			if err := DrainOrphanedVFIOAP(); err != nil {
				t.Fatalf("DrainOrphanedVFIOAP: %v", err)
			}

			for _, q := range tc.wantDrained {
				if got := readOverride(t, root, q); got != "" {
					t.Errorf("queue %s: driver_override = %q, want cleared", q, got)
				}
			}
			for _, q := range tc.wantKept {
				want := tc.overrides[q]
				if got := readOverride(t, root, q); got != want {
					t.Errorf("queue %s: driver_override = %q, want %q untouched", q, got, want)
				}
			}
		})
	}
}

// A missing matrix root means the vfio_ap module holds no mdevs: the drain
// proceeds and the orphan comes back to zcrypt.
func TestDrainOrphanedVFIOAP_MissingMatrixRoot(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": "vfio_ap"})
	if err := os.Remove(mdev.VFIOAPMatrixPath); err != nil {
		t.Fatalf("remove matrix root: %v", err)
	}

	if err := DrainOrphanedVFIOAP(); err != nil {
		t.Fatalf("DrainOrphanedVFIOAP: %v", err)
	}
	if got := readOverride(t, root, "01.0002"); got != "" {
		t.Errorf("queue 01.0002: driver_override = %q, want cleared", got)
	}
}

// An unreadable matrix root must abort the drain: an empty live set would
// make every claimed queue look orphaned.
func TestDrainOrphanedVFIOAP_UnreadableMatrixRoot(t *testing.T) {
	root := fakeSysfs(t, map[string]string{"01.0002": "vfio_ap"})
	if err := os.Remove(mdev.VFIOAPMatrixPath); err != nil {
		t.Fatalf("remove matrix root: %v", err)
	}
	// A regular file where the directory should be: ReadDir fails with
	// something other than fs.ErrNotExist.
	writeFile(t, mdev.VFIOAPMatrixPath, "")

	if err := DrainOrphanedVFIOAP(); err == nil {
		t.Fatal("DrainOrphanedVFIOAP: want error when live mdevs cannot be listed")
	}
	if got := readOverride(t, root, "01.0002"); got != "vfio_ap" {
		t.Errorf("queue 01.0002: driver_override = %q, want vfio_ap untouched", got)
	}
}

// TestDrainOrphanedVFIOAPIgnoresDriverAttributes pins that the driver's own
// mdev_supported_types subtree, which sits under the matrix root beside the
// mediated devices and is a real directory rather than a symlink, is not read
// as an mdev. It has no matrix attribute, so treating it as one warned on
// every driver start.
func TestDrainOrphanedVFIOAPIgnoresDriverAttributes(t *testing.T) {
	fakeSysfs(t, map[string]string{"01.0002": ""})
	types := filepath.Join(mdev.VFIOAPMatrixPath, "mdev_supported_types", "vfio_ap-passthrough")
	if err := os.MkdirAll(types, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", types, err)
	}
	buf := captureKlog(t)

	if err := DrainOrphanedVFIOAP(); err != nil {
		t.Fatalf("DrainOrphanedVFIOAP: %v", err)
	}

	klog.Flush()
	if got := buf.String(); strings.Contains(got, "read matrix of mdev") {
		t.Errorf("log mentions reading a driver attribute as an mdev:\n%s", got)
	}
}

// captureKlog redirects klog to a buffer for the duration of the test.
func captureKlog(t *testing.T) *bytes.Buffer {
	t.Helper()

	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	if err := fs.Set("logtostderr", "false"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	klog.SetOutput(&buf)
	t.Cleanup(func() {
		klog.Flush()
		klog.SetOutput(nil)
		if err := fs.Set("logtostderr", "true"); err != nil {
			t.Error(err)
		}
	})
	return &buf
}
