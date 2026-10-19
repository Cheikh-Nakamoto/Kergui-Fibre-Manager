// Package usecase holds the relay module's application logic: settling the
// lease on each heartbeat, handing commands to the lease holder, and taking
// their signed results back.
package usecase

import (
	"context"
	"time"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
)

// Clock is the time source; tests inject one.
type Clock func() time.Time

// OwnerLookup answers whether a user owns the household. It is a port so
// the relay module never queries the kernel's membership tables itself; the
// hub's composition root plugs the kernel's repository in.
type OwnerLookup func(ctx context.Context, tenantID, userID string) (bool, error)

// Heartbeat settles the lease for one relay (RFC-005, design rule 1).
type Heartbeat struct {
	Leases  domain.LeaseRepository
	Trust   TrustReader
	IsOwner OwnerLookup
	Now     Clock
	Timings domain.Timings
}

// TrustReader is the slice of the agents module the relay needs: the
// household's registered network, and the versions a relay should refresh.
type TrustReader interface {
	GetNetwork(ctx context.Context, tenantID string) (agentsdomain.Network, error)
	GetBundle(ctx context.Context, tenantID string) (agentsdomain.TrustBundle, error)
	GetCredentials(ctx context.Context, tenantID, agentID string) (agentsdomain.SealedCredentials, error)
}

// HeartbeatResult is what the relay is told: whether it holds the box, for
// how long, and whether its trust material is out of date.
type HeartbeatResult struct {
	State             domain.State
	Epoch             int64
	TTL               time.Duration
	RetryAfter        time.Duration
	Reason            string
	BundleVersion     uint64
	CredentialVersion uint64
}

// Execute records the relay's report and answers with its lease state.
//
// Eligibility is decided here, from the household's registered network: a
// relay on the wrong Wi-Fi, or in front of another box, stays in standby and
// never logs in (RFC-005, design rule 2).
func (uc Heartbeat) Execute(ctx context.Context, agent *agentsdomain.Agent, rep domain.Report) (HeartbeatResult, error) {
	now := uc.Now().UTC()
	network, err := uc.Trust.GetNetwork(ctx, agent.TenantID)
	if err != nil {
		return HeartbeatResult{}, err
	}
	eligible, reason := domain.Eligibility(network, rep)

	isOwner := false
	if agent.UserID != "" && uc.IsOwner != nil {
		if isOwner, err = uc.IsOwner(ctx, agent.TenantID, agent.UserID); err != nil {
			return HeartbeatResult{}, err
		}
	}
	me := domain.Presence{
		AgentID: agent.ID, Kind: agent.Kind, IsOwner: isOwner,
		Eligible: eligible, Reason: reason, SeenAt: now,
	}

	out, err := uc.Leases.Heartbeat(ctx, agent.TenantID, me, rep, now,
		func(cur domain.Lease, me domain.Presence, others []domain.Presence) domain.Outcome {
			return domain.Decide(cur, me, others, rep, now, uc.Timings)
		})
	if err != nil {
		return HeartbeatResult{}, err
	}

	res := HeartbeatResult{
		State: out.State, Epoch: out.Epoch, RetryAfter: out.RetryAfter, Reason: out.Reason,
	}
	if out.State == domain.StateActive {
		res.TTL = out.ExpiresAt.Sub(now)
	}
	// The relay refreshes its trust material when a version moves; it does
	// not poll for it.
	if b, err := uc.Trust.GetBundle(ctx, agent.TenantID); err == nil {
		res.BundleVersion = b.Version
	}
	if c, err := uc.Trust.GetCredentials(ctx, agent.TenantID, agent.ID); err == nil {
		res.CredentialVersion = c.Version
	}
	return res, nil
}
