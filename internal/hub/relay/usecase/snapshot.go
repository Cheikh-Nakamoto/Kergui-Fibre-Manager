package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// SnapshotSink is told about a snapshot once it has been verified and
// stored. It is a port so the relay module keeps carrying bytes and knows
// nothing about devices: the inventory module reads the observations, and the
// hub's composition root plugs the two together.
//
// A sink that fails does not fail the push: the relay has done its job, the
// snapshot is stored, and the next one comes in a minute.
type SnapshotSink interface {
	Observations(ctx context.Context, tenantID, agentID string, signed trust.SignedSnapshot) error
}

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
	// Sink is optional; without it the hub stores snapshots and derives
	// nothing from them.
	Sink SnapshotSink
	Log  Logger
}

// Logger is the slice of a logger this module uses.
type Logger interface {
	Warn(msg string, args ...any)
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
	if err := uc.Repo.Put(ctx, domain.Snapshot{
		TenantID: agent.TenantID, Kind: snap.Kind, AgentID: agent.ID,
		Payload: payload, AsOf: snap.AsOf,
	}); err != nil {
		return err
	}
	if uc.Sink != nil {
		if err := uc.Sink.Observations(ctx, agent.TenantID, agent.ID, signed); err != nil && uc.Log != nil {
			uc.Log.Warn("relay: the inventory refused a snapshot", "tenant_id", agent.TenantID, "error", err)
		}
	}
	return nil
}

// Latest returns the newest snapshot of a kind for a household.
func (uc Snapshots) Latest(ctx context.Context, tenantID, kind string) (domain.Snapshot, error) {
	return uc.Repo.Latest(ctx, tenantID, kind)
}
