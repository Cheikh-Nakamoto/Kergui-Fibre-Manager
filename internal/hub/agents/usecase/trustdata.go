package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// maxBlobBytes caps what the hub will store on a household's behalf. A trust
// bundle holds a handful of keys; anything near this is a mistake or an
// attempt to use the hub as storage.
const maxBlobBytes = 64 * 1024

// Agents lists and revokes a household's relays.
type Agents struct {
	Repo domain.Repository
	Now  Clock
}

// List returns the household's agents, newest first.
func (uc Agents) List(ctx context.Context, tenantID string) ([]*domain.Agent, error) {
	return uc.Repo.ListByTenant(ctx, tenantID)
}

// Revoke ends an agent's access. Its token stops working at once; any
// command it is already running will have its result refused by the lease
// epoch check.
func (uc Agents) Revoke(ctx context.Context, tenantID, id string) error {
	return uc.Repo.Revoke(ctx, tenantID, id, uc.Now().UTC())
}

// TrustMaterial stores what the owner signs or seals: the bundle, the sealed
// box credentials and the network signature. The hub validates shape and
// monotonicity only — it is in no position to judge the contents, and must
// not be able to roll them back.
type TrustMaterial struct {
	Repo   domain.TrustRepository
	Agents domain.Repository
}

// PutBundle stores a bundle the owner signed.
//
// The hub parses it for two reasons only: to read the version it must not go
// back on, and to check the household matches the URL. It never checks the
// signature for the relay's benefit — the relay does that against the key it
// pinned at pairing, which is the whole point of the design.
func (tm TrustMaterial) PutBundle(ctx context.Context, tenantID string, signed trust.Signed[trust.Bundle]) error {
	if len(signed.Payload) == 0 || len(signed.Sig) == 0 {
		return fmt.Errorf("%w: a trust bundle must be signed", trust.ErrUnsigned)
	}
	if len(signed.Payload) > maxBlobBytes {
		return fmt.Errorf("agents: trust bundle is too large (%d bytes)", len(signed.Payload))
	}
	b, err := signed.Unverified()
	if err != nil {
		return err
	}
	if b.TenantID != tenantID {
		return fmt.Errorf("%w: bundle is for another household", trust.ErrTenant)
	}
	if b.Version == 0 {
		return fmt.Errorf("agents: a trust bundle must carry a version")
	}
	return tm.Repo.PutBundle(ctx, domain.TrustBundle{
		TenantID: tenantID, Version: b.Version, Payload: signed.Payload, Sig: signed.Sig,
	})
}

// GetBundle returns the stored bundle.
func (tm TrustMaterial) GetBundle(ctx context.Context, tenantID string) (domain.TrustBundle, error) {
	return tm.Repo.GetBundle(ctx, tenantID)
}

// PutCredentials stores the box password sealed for one relay. The blob is
// opaque here: it is nacl/box ciphertext from the owner to that relay, and
// the hub has no key for it.
func (tm TrustMaterial) PutCredentials(ctx context.Context, tenantID, agentID string, blob []byte, version uint64) error {
	if len(blob) == 0 || len(blob) > maxBlobBytes {
		return fmt.Errorf("agents: sealed credentials must be between 1 and %d bytes", maxBlobBytes)
	}
	if version == 0 {
		return fmt.Errorf("agents: sealed credentials must carry a version")
	}
	if _, err := tm.Agents.ByID(ctx, tenantID, agentID); err != nil {
		return err
	}
	return tm.Repo.PutCredentials(ctx, domain.SealedCredentials{
		TenantID: tenantID, AgentID: agentID, Blob: blob, Version: version,
	})
}

// GetCredentials returns the sealed blob for one relay.
func (tm TrustMaterial) GetCredentials(ctx context.Context, tenantID, agentID string) (domain.SealedCredentials, error) {
	return tm.Repo.GetCredentials(ctx, tenantID, agentID)
}

// PutNetwork records the Wi-Fi signature and the box's certificate pin the
// relays are checked against.
func (tm TrustMaterial) PutNetwork(ctx context.Context, n domain.Network) error {
	if n.RouterPin == "" && len(n.SSIDs) == 0 {
		return fmt.Errorf("agents: a network needs at least a router pin or an SSID")
	}
	return tm.Repo.PutNetwork(ctx, n)
}

// GetNetwork returns the household's network signature.
func (tm TrustMaterial) GetNetwork(ctx context.Context, tenantID string) (domain.Network, error) {
	return tm.Repo.GetNetwork(ctx, tenantID)
}
