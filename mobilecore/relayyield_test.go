package mobilecore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
)

// #94: without a relay running, the phone's own calls take the box whoever
// holds it (the owner's app at home wins); and a relay call waits for the
// box no longer than its lease.

func TestAllowPreempt_WithoutRelayAlwaysEvicts(t *testing.T) {
	c := &Core{}
	if !c.allowPreempt("192.168.1.5(user)") {
		t.Fatal("the owner's app yielded the box to the relay")
	}
}

func TestIOLock_WaitEndsWithTheContext(t *testing.T) {
	var l ioLock
	l.Lock()
	ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Millisecond, agent.ErrLeaseExpired)
	defer cancel()
	start := time.Now()
	if err := l.LockContext(ctx); !errors.Is(err, agent.ErrLeaseExpired) {
		t.Fatalf("LockContext = %v, want the lease's cause", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("the wait outlived its context")
	}
	l.Unlock()
	if err := l.LockContext(context.Background()); err != nil {
		t.Fatalf("LockContext on a free lock = %v", err)
	}
	l.Unlock()
}
