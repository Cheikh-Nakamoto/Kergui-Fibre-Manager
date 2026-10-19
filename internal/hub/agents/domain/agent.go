// Package domain holds the agents module's types and repository contracts:
// the paired relays of a household, the one-shot pairing codes, and the
// opaque blobs the owner signs or seals for them.
//
// The hub stores trust material; it never produces any. The trust bundle and
// the sealed credentials are bytes it cannot forge and, for the credentials,
// cannot read.
package domain

import (
	"context"
	"errors"
	"time"
)

// Kind drives the relay lease priority (RFC-005: a dedicated relay, then the
// owner's phone, then the others).
type Kind string

const (
	// KindDedicated is a phone left plugged in at home for this purpose.
	KindDedicated Kind = "dedicated"
	// KindPC is `kergui serve --hub` on a computer.
	KindPC Kind = "pc"
	// KindPhone is a household member's everyday phone.
	KindPhone Kind = "phone"
)

// Valid reports whether k is a kind the hub knows.
func (k Kind) Valid() bool {
	switch k {
	case KindDedicated, KindPC, KindPhone:
		return true
	}
	return false
}

// Errors the handlers map to status codes.
var (
	ErrNotFound        = errors.New("agents: no such agent")
	ErrPairingNotFound = errors.New("agents: pairing code is unknown, used or expired")
	ErrRevoked         = errors.New("agents: agent is revoked")
	ErrStaleVersion    = errors.New("agents: version is not newer than the stored one")
	ErrNoBundle        = errors.New("agents: this household has no trust bundle yet")
	ErrNoCredentials   = errors.New("agents: no credentials are sealed for this agent")
)

// Agent is a paired relay. The hub keeps its public keys to check the
// signatures on results and snapshots, and the SHA-256 of its token; the
// token itself is shown once, at pairing, and never stored.
type Agent struct {
	ID       string
	TenantID string
	Name     string
	Kind     Kind
	// UserID ties a phone to its member, which is how the lease can prefer
	// the owner's phone. Empty for a dedicated relay or a PC.
	UserID     string
	Ed25519    []byte
	X25519     []byte
	KeyID      string
	CreatedAt  time.Time
	RevokedAt  *time.Time
	LastSeenAt *time.Time
}

// Revoked reports whether this agent may still act.
func (a *Agent) Revoked() bool { return a.RevokedAt != nil }

// Pairing is a one-shot code the owner's app shows as a QR. It is stored
// hashed, expires in minutes and can be redeemed once.
type Pairing struct {
	TenantID  string
	CreatedBy string
	Kind      Kind
	// ForUserID ties the future agent to a member, when the owner is
	// pairing someone else's phone.
	ForUserID string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// TrustBundle is the owner-signed bundle, stored as the bytes the owner
// signed. The hub checks only that the version moves forward: it cannot
// judge the contents, and must not be able to roll them back.
type TrustBundle struct {
	TenantID  string
	Version   uint64
	Payload   []byte
	Sig       []byte
	UpdatedAt time.Time
}

// SealedCredentials is the box password, sealed by the owner for one relay.
// Ciphertext only: the hub has no key for it (RFC-005, Trust model).
type SealedCredentials struct {
	TenantID  string
	AgentID   string
	Blob      []byte
	Version   uint64
	UpdatedAt time.Time
}

// Network is where a relay must be to be eligible: the Wi-Fi the owner
// registered and the box's certificate fingerprint.
type Network struct {
	TenantID  string
	SSIDs     []string
	BSSIDs    []string
	RouterPin string
	UpdatedAt time.Time
}

// Repository stores paired agents.
type Repository interface {
	Create(ctx context.Context, a *Agent, tokenHash []byte) error
	ByID(ctx context.Context, tenantID, id string) (*Agent, error)
	// ByTokenHash resolves a relay's bearer token. It returns revoked
	// agents too, so the caller can answer 401 rather than 404.
	ByTokenHash(ctx context.Context, tokenHash []byte) (*Agent, error)
	ListByTenant(ctx context.Context, tenantID string) ([]*Agent, error)
	Revoke(ctx context.Context, tenantID, id string, at time.Time) error
	TouchSeen(ctx context.Context, id string, at time.Time) error
}

// PairingRepository stores pairing codes. One Postgres type implements it
// together with Repository, so the method is named for what it creates.
type PairingRepository interface {
	CreatePairing(ctx context.Context, codeHash []byte, p Pairing) error
	// Redeem consumes a code atomically: a second caller with the same
	// code gets ErrPairingNotFound, even concurrently.
	Redeem(ctx context.Context, codeHash []byte, now time.Time) (Pairing, error)
}

// TrustRepository stores the per-household trust material.
type TrustRepository interface {
	GetBundle(ctx context.Context, tenantID string) (TrustBundle, error)
	// PutBundle refuses a version that is not strictly newer.
	PutBundle(ctx context.Context, b TrustBundle) error
	GetCredentials(ctx context.Context, tenantID, agentID string) (SealedCredentials, error)
	PutCredentials(ctx context.Context, c SealedCredentials) error
	GetNetwork(ctx context.Context, tenantID string) (Network, error)
	PutNetwork(ctx context.Context, n Network) error
}
