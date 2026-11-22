package agent

import (
	"context"
	"errors"
	"time"
)

// The relay's own lease deadline (issue #55, RFC-005 design rule 1).
//
// The hub's epoch fencing refuses the results and snapshots of a relay that
// lost the lease, but it cannot fence the box session: a relay that stalled
// (Android Doze, a slow box, bad Wi-Fi) and woke up after its lease lapsed
// would log in over the new holder and evict its session. So every box
// operation — login, command, snapshot, auto-reject — is bounded by the
// deadline the relay learnt from its last heartbeat: none starts once it has
// passed, and one still running is cancelled when it comes. A gap between two
// holders is correct; an overlap never is.

// ErrLeaseExpired refuses box work past the relay's local lease deadline.
var ErrLeaseExpired = errors.New("agent: the lease has run out, the box is left alone until the next heartbeat")

// leaseTTL is how long the heartbeat's lease runs. The milliseconds are
// preferred (a shortened lease in tests is under a second); a hub that sends
// neither is given RFC-005's ratio, three heartbeats.
func (a *Agent) leaseTTL(hb heartbeatResponse) time.Duration {
	switch {
	case hb.TTLMillis > 0:
		return time.Duration(hb.TTLMillis) * time.Millisecond
	case hb.TTLSeconds > 0:
		return time.Duration(hb.TTLSeconds) * time.Second
	default:
		return 3 * a.timings.Heartbeat
	}
}

// boxContext bounds one box operation to the lease. It refuses to start
// once the deadline has passed, and the context it returns is cancelled at
// the deadline, with ErrLeaseExpired as the cause.
func (a *Agent) boxContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	left := a.leaseUntil.Sub(a.now())
	if a.leaseUntil.IsZero() || left <= 0 {
		return nil, nil, ErrLeaseExpired
	}
	bctx, cancel := context.WithTimeoutCause(ctx, left, ErrLeaseExpired)
	return bctx, cancel, nil
}
