package agent

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #55: the relay bounds every box operation to its own lease, known
// from the heartbeat's ttl. A relay that stalled past it never logs in over
// the next holder.

// boxCalls wraps the house box and counts what reaches it.
type boxCalls struct {
	next  http.Handler
	calls atomic.Int32
}

func (b *boxCalls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.calls.Add(1)
	b.next.ServeHTTP(w, r)
}

// leaseWorld is a relay that took a 60 s lease at arT0 (fakeHub's ttl).
func leaseWorld(t *testing.T) (*houseWorld, *boxCalls) {
	t.Helper()
	w := newHouseWorld(t, nil)
	if !w.agent.active {
		t.Fatal("the relay did not take the lease")
	}
	calls := &boxCalls{next: w.box}
	w.agent.cfg.Handler = calls
	return w, calls
}

func TestLease_DeadlineComesFromTheHeartbeat(t *testing.T) {
	w, _ := leaseWorld(t)
	if got, want := w.agent.leaseUntil, arT0.Add(60*time.Second); !got.Equal(want) {
		t.Fatalf("lease deadline = %v, want %v (heartbeat sent at arT0, ttl 60 s)", got, want)
	}
	// Milliseconds win over seconds, and a hub that sends neither gets
	// three heartbeats.
	if got := w.agent.leaseTTL(heartbeatResponse{TTLSeconds: 0, TTLMillis: 600}); got != 600*time.Millisecond {
		t.Errorf("ttl from ttlMs = %v", got)
	}
	if got := w.agent.leaseTTL(heartbeatResponse{}); got != 3*w.agent.timings.Heartbeat {
		t.Errorf("ttl with none = %v", got)
	}
}

func TestLease_NoCommandStartsPastTheDeadline(t *testing.T) {
	w, calls := leaseWorld(t)
	w.clock.set(arT0.Add(60 * time.Second)) // the lease ends exactly now
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/devices/" + houseKid + "/block"},
		{"POST", RouteInternetCut},
		{"GET", "/api/usage"},
	} {
		status, out := w.send(c.method, c.path, "")
		if status != http.StatusServiceUnavailable || out["code"] != "lease_expired" {
			t.Errorf("%s %s past the lease = %d %v, want 503 lease_expired", c.method, c.path, status, out)
		}
	}
	if n := calls.calls.Load(); n != 0 {
		t.Fatalf("the box was called %d times past the lease", n)
	}
	if w.box.blocked[houseKid] {
		t.Fatal("the kid was blocked past the lease")
	}
}

func TestLease_NoSnapshotPastTheDeadline(t *testing.T) {
	w, calls := leaseWorld(t)
	w.clock.set(arT0.Add(61 * time.Second))
	w.agent.maybeSnapshot(context.Background(), true)
	if n := calls.calls.Load(); n != 0 {
		t.Fatalf("a snapshot read the box %d times past the lease", n)
	}
}

func TestLease_LongOperationIsCancelledAtTheDeadline(t *testing.T) {
	w, _ := leaseWorld(t)
	// 40 ms of lease left on the relay's clock.
	w.clock.set(arT0.Add(60*time.Second - 40*time.Millisecond))
	var cause atomic.Value
	w.agent.cfg.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		// A box that hangs: only the lease ends the wait.
		select {
		case <-r.Context().Done():
			cause.Store(context.Cause(r.Context()))
			http.Error(rw, "cancelled", http.StatusGatewayTimeout)
		case <-time.After(10 * time.Second):
			t.Error("the box call outlived the lease")
		}
	})
	start := time.Now()
	status, _ := w.send("POST", "/api/devices/"+houseKid+"/block", "")
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the command took %v, want it cut at the lease", took)
	}
	if status < 500 {
		t.Errorf("a command cut at the lease answered %d", status)
	}
	if err, _ := cause.Load().(error); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("the box call was cancelled by %v, want ErrLeaseExpired", err)
	}
}

func TestLease_LostOnAFailedHeartbeat(t *testing.T) {
	w, _ := leaseWorld(t)
	w.agent.hub.base = "http://127.0.0.1:1" // the hub is gone
	w.agent.tick(context.Background())
	if !w.agent.leaseUntil.IsZero() {
		t.Fatalf("lease deadline kept after a failed heartbeat: %v", w.agent.leaseUntil)
	}
}
