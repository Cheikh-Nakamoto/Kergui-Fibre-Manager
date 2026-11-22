package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
)

// leaseScript answers Current with each lease in turn, then the last one.
type leaseScript struct {
	mu     sync.Mutex
	leases []domain.Lease
	calls  int
}

func (s *leaseScript) Heartbeat(context.Context, string, domain.Presence, domain.Report, time.Time,
	func(domain.Lease, domain.Presence, []domain.Presence) domain.Outcome) (domain.Outcome, error) {
	return domain.Outcome{}, errors.New("not used")
}

func (s *leaseScript) Current(context.Context, string) (domain.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := min(s.calls, len(s.leases)-1)
	s.calls++
	return s.leases[i], nil
}

// emptyQueue never has a command; claims records the epochs it was asked for.
type emptyQueue struct{ claims int }

func (q *emptyQueue) Enqueue(context.Context, *domain.Command) error { return nil }
func (q *emptyQueue) Claim(context.Context, string, string, int64, time.Time) (*domain.Command, error) {
	q.claims++
	return nil, domain.ErrNoCommand
}
func (q *emptyQueue) Complete(context.Context, string, string, string, int64, json.RawMessage, time.Time) error {
	return nil
}
func (q *emptyQueue) ByID(context.Context, string, string) (*domain.Command, error) {
	return nil, domain.ErrNoCommand
}
func (q *emptyQueue) Expire(context.Context, time.Time) (int, error) { return 0, nil }

func TestNextFor_LeaseLapsingDuringTheWaitIsNothingToDo(t *testing.T) {
	now := time.Now()
	me := &agentsdomain.Agent{ID: "relay-a", TenantID: "t1"}
	for name, after := range map[string]domain.Lease{
		"lapsed":       {TenantID: "t1", AgentID: "relay-a", Epoch: 4, ExpiresAt: now.Add(-time.Second)},
		"taken over":   {TenantID: "t1", AgentID: "relay-b", Epoch: 5, ExpiresAt: now.Add(time.Minute)},
		"nobody holds": {TenantID: "t1"},
	} {
		t.Run(name, func(t *testing.T) {
			leases := &leaseScript{leases: []domain.Lease{
				{TenantID: "t1", AgentID: "relay-a", Epoch: 4, ExpiresAt: now.Add(time.Minute)},
				after,
			}}
			cmds := &emptyQueue{}
			q := Queue{Commands: cmds, Leases: leases, Waiters: NewWaiters(), Now: time.Now}
			cmd, err := q.NextFor(context.Background(), me, 20*time.Millisecond)
			if err != nil || cmd != nil {
				t.Fatalf("NextFor = %v, %v; want nothing to do", cmd, err)
			}
			if cmds.claims != 1 {
				t.Errorf("claimed %d times, want once: never after the lease lapsed", cmds.claims)
			}
		})
	}
}

func TestNextFor_NotHeldAtTheStartIsRefused(t *testing.T) {
	now := time.Now()
	leases := &leaseScript{leases: []domain.Lease{{TenantID: "t1", AgentID: "relay-b", Epoch: 5, ExpiresAt: now.Add(time.Minute)}}}
	q := Queue{Commands: &emptyQueue{}, Leases: leases, Waiters: NewWaiters(), Now: time.Now}
	_, err := q.NextFor(context.Background(), &agentsdomain.Agent{ID: "relay-a", TenantID: "t1"}, 20*time.Millisecond)
	if !errors.Is(err, domain.ErrNotLeaseHolder) {
		t.Fatalf("NextFor = %v, want ErrNotLeaseHolder", err)
	}
}
