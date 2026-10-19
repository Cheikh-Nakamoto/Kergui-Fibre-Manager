// Package usecase holds the agents module's application logic: handing out a
// pairing code, turning one into a paired relay, listing and revoking agents,
// and storing the household's trust material.
package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// PairingTTL is how long a pairing code stays usable (RFC-005: 5 minutes).
// It is short because the owner and the relay are side by side when they use
// it.
const PairingTTL = 5 * time.Minute

// Clock is the time source; tests inject one to expire a code without
// waiting five minutes.
type Clock func() time.Time

// IssuePairing hands the owner's app a one-shot code for a new relay.
type IssuePairing struct {
	Pairings domain.PairingRepository
	Now      Clock
}

// IssuePairingParams names who is pairing what.
type IssuePairingParams struct {
	TenantID  string
	CreatedBy string
	Kind      domain.Kind
	ForUserID string
}

// IssuePairingResult carries the code. It is the only time it exists in
// readable form, so it goes straight into the response and nowhere else.
type IssuePairingResult struct {
	Code      string
	ExpiresAt time.Time
}

// Execute creates the code and stores only its hash.
func (uc IssuePairing) Execute(ctx context.Context, p IssuePairingParams) (IssuePairingResult, error) {
	if !p.Kind.Valid() {
		return IssuePairingResult{}, fmt.Errorf("agents: unknown relay kind %q", p.Kind)
	}
	code, hash, err := domain.NewPairingCode(domain.Rand)
	if err != nil {
		return IssuePairingResult{}, err
	}
	expires := uc.Now().Add(PairingTTL).UTC()
	err = uc.Pairings.CreatePairing(ctx, hash, domain.Pairing{
		TenantID:  p.TenantID,
		CreatedBy: p.CreatedBy,
		Kind:      p.Kind,
		ForUserID: p.ForUserID,
		ExpiresAt: expires,
	})
	if err != nil {
		return IssuePairingResult{}, err
	}
	return IssuePairingResult{Code: code, ExpiresAt: expires}, nil
}

// Enroll turns a pairing code into a paired relay. It is the only route by
// which an agent ever enters a household.
type Enroll struct {
	Pairings domain.PairingRepository
	Agents   domain.Repository
	Now      Clock
}

// EnrollParams is what the relay sends: the code it was given and the public
// half of the identity it just generated.
type EnrollParams struct {
	Code    string
	Name    string
	Ed25519 []byte
	X25519  []byte
}

// EnrollResult is returned once. The token cannot be read again.
type EnrollResult struct {
	AgentID  string
	TenantID string
	Token    string
	KeyID    string
}

// Execute redeems the code and creates the agent.
//
// Nothing here tells the relay who the owner is: it pinned the owner's
// fingerprint from the QR and will check the trust bundle against it. A hub
// that answered with its own key would be caught at that step, and again by
// the six digits the two screens show.
func (uc Enroll) Execute(ctx context.Context, p EnrollParams) (EnrollResult, error) {
	keys := trust.PublicKeys{Ed25519: p.Ed25519, X25519: p.X25519}
	if keys.KeyID() == "" || len(p.X25519) != 32 {
		return EnrollResult{}, fmt.Errorf("agents: relay public keys are malformed")
	}
	now := uc.Now()
	pairing, err := uc.Pairings.Redeem(ctx, domain.HashSecret(p.Code), now)
	if err != nil {
		return EnrollResult{}, err
	}
	token, tokenHash, err := domain.NewAgentToken(domain.Rand)
	if err != nil {
		return EnrollResult{}, err
	}
	agent := &domain.Agent{
		ID:        uuid.NewString(),
		TenantID:  pairing.TenantID,
		Name:      relayName(p.Name, pairing.Kind),
		Kind:      pairing.Kind,
		UserID:    pairing.ForUserID,
		Ed25519:   p.Ed25519,
		X25519:    p.X25519,
		KeyID:     keys.KeyID(),
		CreatedAt: now.UTC(),
	}
	if err := uc.Agents.Create(ctx, agent, tokenHash); err != nil {
		return EnrollResult{}, err
	}
	return EnrollResult{AgentID: agent.ID, TenantID: agent.TenantID, Token: token, KeyID: agent.KeyID}, nil
}

func relayName(name string, kind domain.Kind) string {
	if name != "" {
		return name
	}
	return string(kind)
}
