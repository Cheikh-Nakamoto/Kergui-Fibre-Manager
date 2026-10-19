package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Snapshots keeps the last state each relay pushed, so the app can open on
// the household's devices without waking the box (RFC-005 H1 serves the last
// known state, with its as_of).
//
// The payload stays as the relay signed it. The hub stores and forwards
// bytes it cannot forge: an app that verifies the signature is not trusting
// the hub for what it shows.
type Snapshots struct {
	Repo domain.SnapshotRepository
	Now  Clock
}

// Push stores a snapshot a relay signed.
func (uc Snapshots) Push(ctx context.Context, agent *agentsdomain.Agent, signed trust.SignedSnapshot) error {
	snap, err := signed.Unverified()
	if err != nil {
		return err
	}
	if snap.TenantID != agent.TenantID {
		return fmt.Errorf("%w: snapshot is for another household", trust.ErrTenant)
	}
	if snap.AgentID != agent.ID {
		return fmt.Errorf("%w: snapshot is signed for another relay", trust.ErrMalformed)
	}
	if snap.Kind == "" {
		return fmt.Errorf("%w: snapshot has no kind", trust.ErrMalformed)
	}
	payload, err := json.Marshal(signed)
	if err != nil {
		return fmt.Errorf("relay: encode snapshot: %w", err)
	}
	return uc.Repo.Put(ctx, domain.Snapshot{
		TenantID: agent.TenantID, Kind: snap.Kind, AgentID: agent.ID,
		Payload: payload, AsOf: snap.AsOf,
	})
}

// Latest returns the newest snapshot of a kind for a household.
func (uc Snapshots) Latest(ctx context.Context, tenantID, kind string) (domain.Snapshot, error) {
	return uc.Repo.Latest(ctx, tenantID, kind)
}
