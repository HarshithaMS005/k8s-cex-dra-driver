package health

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

const testStaleAfter = 50 * time.Millisecond

// newServer builds a server without a listener: Check reads the heartbeat and
// the threshold only, so the four answers are testable without a port.
func newServer(hb *Heartbeat) *Server {
	return &Server{heartbeat: hb, staleAfter: testStaleAfter}
}

func check(t *testing.T, s *Server, service string) (*grpc_health_v1.HealthCheckResponse, error) {
	t.Helper()
	return s.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{Service: service})
}

func TestCheckServingWhileHeartbeatIsFresh(t *testing.T) {
	s := newServer(NewHeartbeat())

	for _, service := range []string{"", "liveness"} {
		resp, err := check(t, s, service)
		if err != nil {
			t.Fatalf("Check(%q): %v", service, err)
		}
		if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Errorf("Check(%q) = %v, want SERVING", service, resp.GetStatus())
		}
	}
}

func TestCheckNotServingWhenHeartbeatIsStale(t *testing.T) {
	hb := NewHeartbeat()
	hb.stampAt(time.Now().Add(-10 * testStaleAfter))

	resp, err := check(t, newServer(hb), "liveness")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Errorf("Check = %v, want NOT_SERVING", resp.GetStatus())
	}
}

// TestCheckNilHeartbeatServes pins that a server started without a heartbeat
// answers SERVING instead of panicking: a nil heartbeat has nothing to
// observe, matching Stamp's nil no-op.
func TestCheckNilHeartbeatServes(t *testing.T) {
	resp, err := check(t, newServer(nil), "liveness")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("Check = %v, want SERVING", resp.GetStatus())
	}
}

// TestCheckUnknownServiceIsNotFound covers readiness, which has no
// implementation yet: an unknown name must be NotFound rather than a healthy
// answer for a check nobody wrote.
func TestCheckUnknownServiceIsNotFound(t *testing.T) {
	if _, err := check(t, newServer(NewHeartbeat()), "readiness"); status.Code(err) != codes.NotFound {
		t.Fatalf("Check(readiness) error = %v, want NotFound", err)
	}
}

// TestStartDisabledByNegativePort pins that the disabled service is a usable
// nil, so a caller never branches on the port.
func TestStartDisabledByNegativePort(t *testing.T) {
	s, err := Start(-1, NewHeartbeat(), testStaleAfter)
	if err != nil {
		t.Fatalf("Start(-1): %v", err)
	}
	if s != nil {
		t.Fatalf("Start(-1) = %v, want nil server", s)
	}
	if addr := s.Addr(); addr != "" {
		t.Errorf("Addr on a disabled service = %q, want empty", addr)
	}
	s.Stop()
}

// TestStartRandomPortServes takes the real path: a listener, a served gRPC
// health service, and a shutdown that returns.
//
// It binds loopback rather than every interface, and skips outright when even
// that is refused: the nix build sandbox denies networking wholesale, so this
// case runs in a dev shell and in `make check` but not inside `nix flake
// check`. Everything the check itself decides is covered by the cases above,
// which need no socket.
func TestStartRandomPortServes(t *testing.T) {
	old := listenHost
	listenHost = "127.0.0.1"
	t.Cleanup(func() { listenHost = old })

	s, err := Start(0, NewHeartbeat(), testStaleAfter)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skipf("sandbox refuses a listener: %v", err)
	}
	if err != nil {
		t.Fatalf("Start(0): %v", err)
	}
	t.Cleanup(s.Stop)

	if s.Addr() == "" {
		t.Fatal("Addr after Start(0) is empty, want the resolved port")
	}
	resp, err := check(t, s, "liveness")
	if err != nil || resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("Check on a started server = %v, %v; want SERVING", resp.GetStatus(), err)
	}
}

func TestStaleAfterTracksScanInterval(t *testing.T) {
	if got, want := StaleAfter(30*time.Second), 5*time.Minute; got != want {
		t.Errorf("StaleAfter(30s) = %s, want %s", got, want)
	}
}

// TestWatchLogsBothEdges pins the watchdog's contract: one line when the
// heartbeat goes stale, one when it recovers, and nothing while it is fresh.
// The lines are forensics that survive into a restarted pod's previous logs.
func TestWatchLogsBothEdges(t *testing.T) {
	buf := captureKlog(t)
	hb := NewHeartbeat()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(ctx, hb, testStaleAfter)
	}()

	waitFor(t, buf, "not making progress", "the stale edge")

	// Staying stale must not repeat the line: it marks the transition, not
	// the state. Four thresholds is many watchdog ticks.
	time.Sleep(4 * testStaleAfter)
	if got := countLines(buf.String(), "W", "not making progress"); got != 1 {
		t.Errorf("stale-edge lines while stale = %d, want exactly 1 (the edge, not the state)", got)
	}

	// Keep stamping until the recovery edge lands, so the heartbeat cannot
	// go stale again inside the wait and blur the count.
	stamping := make(chan struct{})
	stopStamping := make(chan struct{})
	go func() {
		defer close(stamping)
		for {
			select {
			case <-stopStamping:
				return
			default:
				hb.Stamp()
				time.Sleep(testStaleAfter / 10)
			}
		}
	}()
	waitFor(t, buf, "completing again", "the recovery edge")
	close(stopStamping)
	<-stamping

	cancel()
	<-done
}

func TestWatchSilentWhileFresh(t *testing.T) {
	buf := captureKlog(t)
	hb := NewHeartbeat()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		Watch(ctx, hb, testStaleAfter)
	}()

	deadline := time.Now().Add(4 * testStaleAfter)
	for time.Now().Before(deadline) {
		hb.Stamp()
		time.Sleep(testStaleAfter / 10)
	}
	cancel()
	<-done

	klog.Flush()
	if got := buf.String(); got != "" {
		t.Errorf("watchdog logged while the heartbeat stayed fresh:\n%s", got)
	}
}

// countLines counts log lines of one klog severity containing want. klog
// writes a message to its own severity stream and every lower one, so a single
// warning reaches a single captured writer twice; the severity prefix is what
// tells the two copies apart.
func countLines(log, severity, want string) int {
	n := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, severity) && strings.Contains(line, want) {
			n++
		}
	}
	return n
}

// waitFor blocks until the captured log contains want, so the test follows the
// watchdog's own ticker instead of guessing how long an edge takes.
func waitFor(t *testing.T, buf *syncBuffer, want, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * testStaleAfter)
	for time.Now().Before(deadline) {
		klog.Flush()
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(testStaleAfter / 10)
	}
	t.Fatalf("timed out waiting for %s (%q) in:\n%s", what, want, buf.String())
}

// syncBuffer collects klog output written from the watchdog goroutine while
// the test reads it, which a bare bytes.Buffer cannot survive.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureKlog redirects klog to a buffer for the duration of the test.
func captureKlog(t *testing.T) *syncBuffer {
	t.Helper()

	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	if err := fs.Set("logtostderr", "false"); err != nil {
		t.Fatal(err)
	}
	// klog otherwise writes a warning to its own stream and to every lower
	// one, which reaches a single captured writer as two identical lines.
	if err := fs.Set("one_output", "true"); err != nil {
		t.Fatal(err)
	}
	var buf syncBuffer
	klog.SetOutput(&buf)
	t.Cleanup(func() {
		klog.Flush()
		klog.SetOutput(nil)
		if err := fs.Set("logtostderr", "true"); err != nil {
			t.Error(err)
		}
		if err := fs.Set("one_output", "false"); err != nil {
			t.Error(err)
		}
	})
	return &buf
}
