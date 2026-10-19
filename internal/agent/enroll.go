package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// EnrollResult is what the person pairing a relay must check.
type EnrollResult struct {
	AgentID  string
	TenantID string
	// Code is the six digits shown on both screens. The owner compares
	// them and only then signs the relay into the trust bundle. If they
	// differ, a hub swapped a key and pairing must be abandoned.
	Code string
}

// Enroll joins a household: it generates this relay's keys, redeems the
// pairing code, pins the owner's key from the invite, and returns the six
// digits to compare.
//
// Nothing here trusts the hub. The owner's key comes from the bundle, but
// it is accepted only if its fingerprint matches the one in the QR, which
// the owner's own app produced. The six digits then cover both parties'
// keys, so a hub that substituted either is caught by a person looking at
// two screens.
func Enroll(ctx context.Context, hubURL string, invite trust.Invite, name, kind string, client *http.Client) (StateData, EnrollResult, error) {
	id, err := trust.Generate(rand.Reader)
	if err != nil {
		return StateData{}, EnrollResult{}, err
	}
	pub := id.Public()
	timings := DefaultTimings()
	hub := newHubClient(hubURL, "", client, timings)

	var redeemed struct {
		AgentID  string `json:"agentId"`
		TenantID string `json:"tenantId"`
		Token    string `json:"token"`
		KeyID    string `json:"keyId"`
	}
	redeemCtx, cancel := context.WithTimeout(ctx, timings.HubTimeout)
	defer cancel()
	err = hub.call(redeemCtx, http.MethodPost, "/relay/v1/pair/redeem", map[string]any{
		"code": invite.Code, "name": name, "ed25519": pub.Ed25519, "x25519": pub.X25519,
	}, &redeemed)
	if err != nil {
		return StateData{}, EnrollResult{}, err
	}

	// From here the relay is authenticated and can read the bundle, which
	// is where the owner's key comes from.
	hub = newHubClient(hubURL, redeemed.Token, client, timings)
	signed, err := hub.bundle(ctx)
	if err != nil {
		return StateData{}, EnrollResult{}, fmt.Errorf("agent: the household has no trust bundle yet, ask the owner to publish one: %w", err)
	}
	bundle, err := signed.Unverified()
	if err != nil {
		return StateData{}, EnrollResult{}, err
	}
	if bundle.Owner.KeyID() != invite.OwnerFingerprint {
		return StateData{}, EnrollResult{}, fmt.Errorf(
			"%w: the hub offered key %s, the QR says %s — do not continue",
			trust.ErrUnknownKey, bundle.Owner.KeyID(), invite.OwnerFingerprint)
	}
	// Now that the key is pinned, check the bundle really is signed by it.
	verifier := trust.NewVerifier(redeemed.TenantID, bundle.Owner.SigningKey(), time.Now())
	if err := verifier.AcceptBundle(signed); err != nil {
		return StateData{}, EnrollResult{}, err
	}

	state := StateData{
		HubURL:   hubURL,
		AgentID:  redeemed.AgentID,
		TenantID: redeemed.TenantID,
		Token:    redeemed.Token,
		Identity: id.Marshal(),
		OwnerKey: bundle.Owner.SigningKey(),
		Bundle:   signed,
		identity: id,
	}
	return state, EnrollResult{
		AgentID: redeemed.AgentID, TenantID: redeemed.TenantID,
		Code: trust.PairingCode(bundle.Owner, pub),
	}, nil
}
