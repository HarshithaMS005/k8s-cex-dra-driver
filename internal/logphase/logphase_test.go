package logphase

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	"k8s.io/klog/v2"
)

// captureKlog redirects klog to a buffer for one test. Every package in the
// driver now renders its log lines through this one, so the prefix shape and
// the verbosity gate below are worth pinning.
func captureKlog(t *testing.T, verbosity string) *bytes.Buffer {
	t.Helper()

	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	if err := fs.Set("logtostderr", "false"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("v", verbosity); err != nil {
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
		if err := fs.Set("v", "0"); err != nil {
			t.Error(err)
		}
	})
	return &buf
}

func TestLogfRendersPhasePrefix(t *testing.T) {
	buf := captureKlog(t, "0")

	Logf(ScanLoop, "found %d queues", 18)
	klog.Flush()

	got := buf.String()
	if !strings.Contains(got, "[SCAN-LOOP] found 18 queues") {
		t.Errorf("output = %q, want the phase-prefixed message", got)
	}
	// The line must name the caller, not logphase.go, or every log line in
	// the driver would point here.
	if !strings.Contains(got, "logphase_test.go:") {
		t.Errorf("output = %q, want the caller's file, not logphase.go", got)
	}
}

func TestWarnfLogsAtWarningSeverity(t *testing.T) {
	buf := captureKlog(t, "0")

	Warnf(Preparation, "mdev %s vanished", "abc")
	klog.Flush()

	got := buf.String()
	if !strings.HasPrefix(got, "W") {
		t.Errorf("output = %q, want klog's W severity prefix", got)
	}
	if !strings.Contains(got, "[PREPARATION] mdev abc vanished") {
		t.Errorf("output = %q, want the phase-prefixed message", got)
	}
}

func TestVEnabledTracksVf(t *testing.T) {
	captureKlog(t, "0")
	if VEnabled(4) {
		t.Error("VEnabled(4) at -v=0 = true, want false")
	}

	captureKlog(t, "4")
	if !VEnabled(4) {
		t.Error("VEnabled(4) at -v=4 = false, want true")
	}
}

func TestVfHonoursVerbosity(t *testing.T) {
	quiet := captureKlog(t, "0")
	Vf(4, ScanLoop, "per-queue detail")
	klog.Flush()
	if got := quiet.String(); got != "" {
		t.Errorf("output at -v=0 = %q, want nothing", got)
	}

	loud := captureKlog(t, "4")
	Vf(4, ScanLoop, "per-queue detail")
	klog.Flush()
	if got := loud.String(); !strings.Contains(got, "[SCAN-LOOP] per-queue detail") {
		t.Errorf("output at -v=4 = %q, want the message", got)
	}
}
