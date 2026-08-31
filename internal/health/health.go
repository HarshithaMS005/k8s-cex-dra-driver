// Package health serves the driver's liveness surface. The scan loop stamps a
// heartbeat at the end of every cycle, and a gRPC health service answers
// SERVING for as long as that stamp is fresher than a staleness threshold, so
// a scan wedged on a card that never answers becomes a failed probe and a
// container restart instead of silently stale ResourceSlices.
//
// The check reads the timestamp and nothing else. A check that touched the AP
// hardware would block exactly when it needed to answer, and the kubelet's
// registration protocol - the only other thing observing this process - talks
// to goroutines that keep answering right through a scan wedge.
package health

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"k8s-cex-dra-driver/internal/logphase"
)

// staleMultiple turns the configured scan interval into the staleness
// threshold. Deriving it means the two cannot drift apart when an operator
// raises SCAN_INTERVAL. The multiple is generous on purpose: firing late
// delays a restart, firing early restarts a healthy driver, and ten intervals
// dwarf any latency a card that still answers adds to a cycle, so the
// headroom costs nothing.
const staleMultiple = 10

// watchdogChecksPerThreshold sets how often the watchdog samples the heartbeat,
// as a fraction of the threshold. Four keeps the stale edge inside a quarter of
// the threshold without a timer that fires for its own sake.
const watchdogChecksPerThreshold = 4

// listenHost is the interface the health service binds to. Empty is every
// interface, which is what the kubelet needs: it probes the pod IP, not
// loopback. It is a var, not a const, so a test can bind loopback instead -
// a build sandbox permits that address and no other. Nothing in production
// reassigns it.
var listenHost = ""

// StaleAfter returns the staleness threshold for a scan interval.
func StaleAfter(scanInterval time.Duration) time.Duration {
	return staleMultiple * scanInterval
}

// Heartbeat records when the scan loop last completed a cycle. It is seeded at
// construction rather than at the first stamp, so the initial scan runs inside
// the covered window instead of in a gap before it.
type Heartbeat struct {
	mu   sync.Mutex
	last time.Time
}

// NewHeartbeat returns a heartbeat seeded to now.
func NewHeartbeat() *Heartbeat {
	return &Heartbeat{last: time.Now()}
}

// Stamp records that a scan cycle completed. A cycle that returned an error
// still completed: the error has its own log path, and only a cycle that never
// returns means the loop is gone.
func (h *Heartbeat) Stamp() {
	if h == nil {
		return
	}
	h.stampAt(time.Now())
}

func (h *Heartbeat) stampAt(t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = t
}

// Age reports how long ago the last cycle completed. The lock is held only
// around the field, so a wedged scan loop cannot wedge a caller. A nil
// heartbeat has nothing to observe and reads as always fresh, matching
// Stamp's nil no-op, so Check never panics on a server started without one.
func (h *Heartbeat) Age() time.Duration {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Since(h.last)
}

// Watch logs the edges of heartbeat staleness and returns when ctx is done.
// Steady state is silent; the two lines exist so that the diagnosis survives
// into the restarted pod's previous logs, whether or not the restart the probe
// asks for actually completes.
func Watch(ctx context.Context, hb *Heartbeat, staleAfter time.Duration) {
	ticker := time.NewTicker(staleAfter / watchdogChecksPerThreshold)
	defer ticker.Stop()

	stale := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			age := hb.Age()
			switch {
			case !stale && age > staleAfter:
				stale = true
				logphase.Warnf(logphase.ScanLoop,
					"no scan cycle completed for %s (threshold %s): the scan loop is not making progress",
					age.Truncate(time.Second), staleAfter)
			case stale && age <= staleAfter:
				stale = false
				logphase.Logf(logphase.ScanLoop, "scan cycles are completing again")
			}
		}
	}
}

// Server is the gRPC health service. A nil *Server is a disabled service:
// every method on it is a no-op, so a caller never branches on the port.
type Server struct {
	grpc_health_v1.UnimplementedHealthServer

	heartbeat  *Heartbeat
	staleAfter time.Duration
	server     *grpc.Server
	addr       string
	wg         sync.WaitGroup
}

// Start listens for health checks on port and serves until Stop. A negative
// port disables the service and returns a nil Server; zero takes a random
// port, which suits a test and cannot be probed by a manifest. A listen
// failure is returned rather than logged: a driver whose liveness cannot be
// observed should not start.
func Start(port int, heartbeat *Heartbeat, staleAfter time.Duration) (*Server, error) {
	if port < 0 {
		return nil, nil
	}

	addr := net.JoinHostPort(listenHost, strconv.Itoa(port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for health checks on %s: %w", addr, err)
	}

	s := &Server{
		heartbeat:  heartbeat,
		staleAfter: staleAfter,
		server:     grpc.NewServer(),
		addr:       listener.Addr().String(),
	}
	grpc_health_v1.RegisterHealthServer(s.server, s)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// A stopped server is Stop doing its job, not a failure.
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logphase.Warnf(logphase.Startup, "health service on %s stopped serving: %v", s.addr, err)
		}
	}()

	return s, nil
}

// Addr returns the address the service listens on, resolved when the port was
// zero. Empty for a disabled service.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	return s.addr
}

// Stop shuts the service down and waits for the serving goroutine.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	s.server.GracefulStop()
	s.wg.Wait()
}

// Check implements grpc_health_v1.HealthServer. Liveness is the freshness of
// the last completed scan cycle. Any name other than liveness and the default
// is NotFound, which is what an unimplemented readiness check must answer
// until there is one.
func (s *Server) Check(_ context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	switch req.GetService() {
	case "", "liveness":
	default:
		return nil, status.Errorf(codes.NotFound, "unknown service %q", req.GetService())
	}

	serving := grpc_health_v1.HealthCheckResponse_SERVING
	if s.heartbeat.Age() > s.staleAfter {
		serving = grpc_health_v1.HealthCheckResponse_NOT_SERVING
	}
	return &grpc_health_v1.HealthCheckResponse{Status: serving}, nil
}
