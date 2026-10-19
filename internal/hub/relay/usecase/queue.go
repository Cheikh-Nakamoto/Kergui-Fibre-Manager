package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Timings of the command path.
const (
	// CallerWait is how long the app's request waits for a result before
	// it is told to come back for it (RFC-005: 20 s, then 202).
	CallerWait = 20 * time.Second
	// pollInterval is the safety net behind the in-memory wake-up: with
	// several hub instances, the relay long-polling instance A does not
	// hear the channel of instance B.
	pollInterval = 2 * time.Second
)

// Waiters wakes the goroutines blocked on something: a relay long-polling
// for the next command of its household, or an app request waiting for a
// result. It is in-memory on purpose — it is an optimisation, and every
// waiter also polls.
type Waiters struct {
	mu sync.Mutex
	ch map[string]chan struct{}
}

// NewWaiters builds an empty set.
func NewWaiters() *Waiters { return &Waiters{ch: map[string]chan struct{}{}} }

// Wait returns a channel closed when key is signalled, and a function to
// release it. The caller must always call the release function.
func (w *Waiters) Wait(key string) (<-chan struct{}, func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.ch[key]
	if !ok {
		c = make(chan struct{})
		w.ch[key] = c
	}
	return c, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		// Only drop the entry if it is still the one we waited on: a
		// signal in between replaced it.
		if cur, ok := w.ch[key]; ok && cur == c {
			delete(w.ch, key)
		}
	}
}

// Signal wakes everyone waiting on key.
func (w *Waiters) Signal(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.ch[key]; ok {
		close(c)
		delete(w.ch, key)
	}
}

// Queue carries signed commands between the app and the household's relay.
// The hub never signs anything here: it stores the owner's bytes and hands
// them over.
type Queue struct {
	Commands domain.CommandRepository
	Leases   domain.LeaseRepository
	Waiters  *Waiters
	Now      Clock
}

// SubmitParams is one signed command, with the route it arrived on.
type SubmitParams struct {
	TenantID     string
	IssuerUserID string
	Signed       trust.SignedCommand
	Body         []byte
	// WantMethod and WantPath are what the REST route promises. The signed
	// command must match them, so a command signed for one action cannot be
	// posted to another route.
	WantMethod string
	WantPath   string
}

// Submit queues a command and returns its id.
func (q Queue) Submit(ctx context.Context, p SubmitParams) (string, error) {
	cmd, err := p.Signed.Unverified()
	if err != nil {
		return "", err
	}
	if cmd.TenantID != p.TenantID {
		return "", fmt.Errorf("%w: the command is signed for another household", trust.ErrTenant)
	}
	if cmd.Method != p.WantMethod || cmd.Path != p.WantPath {
		return "", fmt.Errorf("%w: the command is signed for %s %s", trust.ErrScope, cmd.Method, cmd.Path)
	}
	if cmd.BodySHA256 != trust.BodyDigest(p.Body) {
		return "", trust.ErrBodyMismatch
	}
	now := q.Now().UTC()
	if !now.Before(cmd.ExpiresAt) {
		return "", trust.ErrExpired
	}
	envelope, err := json.Marshal(p.Signed)
	if err != nil {
		return "", fmt.Errorf("relay: encode command: %w", err)
	}
	c := &domain.Command{
		// The signed cmd_id is the command's identity everywhere: the
		// queue row, the URL the app polls, and the id the relay signs
		// its result under.
		ID:           cmd.CmdID,
		TenantID:     p.TenantID,
		IssuerUserID: p.IssuerUserID,
		Envelope:     envelope,
		Body:         p.Body,
		Method:       cmd.Method,
		Path:         cmd.Path,
		CreatedAt:    now,
		ExpiresAt:    cmd.ExpiresAt,
	}
	if err := q.Commands.Enqueue(ctx, c); err != nil {
		return "", err
	}
	q.Waiters.Signal(tenantKey(p.TenantID))
	return c.ID, nil
}

// NextFor is the relay's long poll: it returns the next command for the
// lease holder, or nil when the wait runs out.
//
// Only the lease holder is served. A relay that lost the lease but is still
// polling gets ErrNotLeaseHolder rather than a command it would be refused
// the right to report on.
func (q Queue) NextFor(ctx context.Context, agent *agentsdomain.Agent, wait time.Duration) (*domain.Command, error) {
	deadline := q.Now().Add(wait)
	key := tenantKey(agent.TenantID)
	for {
		lease, err := q.Leases.Current(ctx, agent.TenantID)
		if err != nil {
			return nil, err
		}
		if !lease.Held(q.Now()) || lease.AgentID != agent.ID {
			return nil, domain.ErrNotLeaseHolder
		}
		cmd, err := q.Commands.Claim(ctx, agent.TenantID, agent.ID, lease.Epoch, q.Now().UTC())
		if err == nil {
			return cmd, nil
		}
		if err != domain.ErrNoCommand {
			return nil, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, nil
		}
		woken, release := q.Waiters.Wait(key)
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-woken:
		case <-time.After(min(left, pollInterval)):
		}
		release()
	}
}

// Complete records a relay's signed result and wakes the caller waiting on
// it.
func (q Queue) Complete(ctx context.Context, agent *agentsdomain.Agent, cmdID string, result trust.SignedResult) error {
	lease, err := q.Leases.Current(ctx, agent.TenantID)
	if err != nil {
		return err
	}
	if lease.AgentID != agent.ID {
		return domain.ErrNotLeaseHolder
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("relay: encode result: %w", err)
	}
	if err := q.Commands.Complete(ctx, agent.TenantID, cmdID, agent.ID, lease.Epoch, raw, q.Now().UTC()); err != nil {
		return err
	}
	q.Waiters.Signal(commandKey(cmdID))
	return nil
}

// AwaitResult blocks until the command is finished or wait runs out. A
// caller that times out is given the command id instead, and polls.
func (q Queue) AwaitResult(ctx context.Context, tenantID, cmdID string, wait time.Duration) (*domain.Command, error) {
	deadline := q.Now().Add(wait)
	key := commandKey(cmdID)
	for {
		c, err := q.Commands.ByID(ctx, tenantID, cmdID)
		if err != nil {
			return nil, err
		}
		if c.Status == domain.StatusDone || c.Status == domain.StatusExpired {
			return c, nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			return c, nil
		}
		woken, release := q.Waiters.Wait(key)
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-woken:
		case <-time.After(min(left, pollInterval)):
		}
		release()
	}
}

// Status returns one command and its result.
func (q Queue) Status(ctx context.Context, tenantID, cmdID string) (*domain.Command, error) {
	return q.Commands.ByID(ctx, tenantID, cmdID)
}

func tenantKey(tenantID string) string { return "tenant:" + tenantID }
func commandKey(cmdID string) string   { return "command:" + cmdID }
