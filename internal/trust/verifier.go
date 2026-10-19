package trust

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Verifier defaults. They are fields so a test can shorten them, not knobs
// for a caller to loosen.
const (
	// DefaultClockSkew is how far ahead of us a signer's clock may be. A
	// phone and a VPS drift; 5 s is enough and still far below the 60 s
	// lifetime of a command.
	DefaultClockSkew = 5 * time.Second
	// DefaultHubBlockLimit and DefaultHubBlockWindow rate-limit the
	// WhatsApp "1 = block" key (RFC-005).
	DefaultHubBlockLimit  = 5
	DefaultHubBlockWindow = time.Hour
)

// Verifier is the relay's side of the trust model: the owner key pinned at
// pairing, the newest bundle, and the memory needed to refuse a replay.
//
// It is safe for concurrent use: the agent verifies commands from its
// long-poll loop while the heartbeat loop installs a new bundle.
type Verifier struct {
	// ClockSkew, HubBlockLimit and HubBlockWindow may be changed before the
	// verifier is used.
	ClockSkew      time.Duration
	HubBlockLimit  int
	HubBlockWindow time.Duration

	tenantID  string
	ownerKey  ed25519.PublicKey
	startedAt time.Time

	mu        sync.Mutex
	bundle    *Bundle
	seen      map[string]time.Time // cmd_id -> expiry, purged as it goes
	protected map[string]bool      // MACs this host adds, e.g. the relay's own
	hubBlocks []time.Time
}

// NewVerifier pins the owner's signing key, which is the root of trust: it
// comes from the QR code at pairing, not from the hub. startedAt is the
// moment the relay came up; a command issued before that is refused, which
// is what lets the replay cache stay in memory.
func NewVerifier(tenantID string, owner ed25519.PublicKey, startedAt time.Time) *Verifier {
	return &Verifier{
		ClockSkew:      DefaultClockSkew,
		HubBlockLimit:  DefaultHubBlockLimit,
		HubBlockWindow: DefaultHubBlockWindow,
		tenantID:       tenantID,
		ownerKey:       append(ed25519.PublicKey(nil), owner...),
		startedAt:      startedAt.UTC(),
		seen:           map[string]time.Time{},
		protected:      map[string]bool{},
	}
}

// AcceptBundle installs a bundle after checking the owner signed it, that it
// is for this household, and that its version moved forward.
func (v *Verifier) AcceptBundle(sb Signed[Bundle]) error {
	b, err := sb.Open(DomainBundle, v.ownerKey)
	if err != nil {
		return err
	}
	if b.TenantID != v.tenantID {
		return fmt.Errorf("%w: bundle is for %s", ErrTenant, b.TenantID)
	}
	// The owner key inside the bundle must be the one we pinned, otherwise a
	// hub could hand us a bundle that quietly hands the household over.
	if !b.Owner.signPub().Equal(v.ownerKey) {
		return fmt.Errorf("%w: bundle names another owner key", ErrUnknownKey)
	}
	if err := b.valid(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bundle != nil && b.Version <= v.bundle.Version {
		return fmt.Errorf("%w: version %d, current %d", ErrStaleBundle, b.Version, v.bundle.Version)
	}
	v.bundle = &b
	return nil
}

// Bundle returns the bundle in force, or nil before the first one arrives.
func (v *Verifier) Bundle() *Bundle {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.bundle
}

// Protect marks a MAC as untouchable for delegated keys on this host, on top
// of the owner's list. The relay protects itself this way: cutting the device
// that carries the commands would end the household's remote access.
func (v *Verifier) Protect(mac string) {
	if n := normaliseMAC(mac); n != "" {
		v.mu.Lock()
		v.protected[n] = true
		v.mu.Unlock()
	}
}

// OwnerKeyID names the pinned owner key.
func (v *Verifier) OwnerKeyID() string { return KeyID(v.ownerKey) }

// VerifyCommand decides whether a command may run, in the order RFC-005
// lists: signature, revocation, delegation and scope, delegation expiry,
// command expiry, replay, then tenant and body.
//
// A command that passes is recorded as used, so the same call twice refuses
// the second time. What the command may reach — the relay allow-list — is a
// separate decision, made by the caller.
func (v *Verifier) VerifyCommand(sc SignedCommand, body []byte, now time.Time) (Command, error) {
	claimed, err := sc.Unverified()
	if err != nil {
		return Command{}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bundle == nil {
		return Command{}, ErrNoBundle
	}

	// 1. Signature, against the key the command names.
	pub, err := v.publicKeyLocked(claimed.KeyID)
	if err != nil {
		return Command{}, err
	}
	cmd, err := sc.Open(DomainCommand, pub)
	if err != nil {
		return Command{}, err
	}

	// 2. Revocation.
	if v.bundle.isRevoked(cmd.KeyID) {
		return Command{}, fmt.Errorf("%w: %s", ErrRevoked, cmd.KeyID)
	}

	// 3 and 4. Delegation, scope, and its expiry.
	if err := v.authorizeLocked(cmd, now); err != nil {
		return Command{}, err
	}

	// 5. Freshness.
	if err := v.freshLocked(cmd, now); err != nil {
		return Command{}, err
	}

	// 6. Replay.
	v.purgeLocked(now)
	if _, used := v.seen[cmd.CmdID]; used {
		return Command{}, fmt.Errorf("%w: %s", ErrReplay, cmd.CmdID)
	}

	// 7. Household and body.
	if cmd.TenantID != v.tenantID {
		return Command{}, fmt.Errorf("%w: command is for %s", ErrTenant, cmd.TenantID)
	}
	if cmd.BodySHA256 != BodyDigest(body) {
		return Command{}, ErrBodyMismatch
	}

	v.seen[cmd.CmdID] = cmd.ExpiresAt
	if v.isHubBlockLocked(cmd.KeyID) {
		v.hubBlocks = append(v.hubBlocks, now)
	}
	return cmd, nil
}

// publicKeyLocked finds the signing key a command names. Only the owner and
// the delegations may issue commands: a relay's key signs results, never
// orders.
func (v *Verifier) publicKeyLocked(keyID string) (ed25519.PublicKey, error) {
	if keyID == "" {
		return nil, ErrUnknownKey
	}
	if keyID == KeyID(v.ownerKey) {
		return v.ownerKey, nil
	}
	if d, ok := v.bundle.delegation(keyID); ok {
		return d.Keys.signPub(), nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnknownKey, keyID)
}

// authorizeLocked applies the scope rules.
func (v *Verifier) authorizeLocked(cmd Command, now time.Time) error {
	if cmd.KeyID == KeyID(v.ownerKey) {
		return nil // the owner is the root of trust
	}
	if v.bundle.OwnerOnly {
		return ErrOwnerOnly
	}
	d, ok := v.bundle.delegation(cmd.KeyID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownKey, cmd.KeyID)
	}
	if !d.ExpiresAt.IsZero() && !now.Before(d.ExpiresAt) {
		return fmt.Errorf("%w: %s", ErrDelegationExpired, d.KeyID())
	}
	switch d.Scope {
	case ScopeControl:
		return nil
	case ScopeView:
		if !isRead(cmd.Method) {
			return fmt.Errorf("%w: view may not %s", ErrScope, cmd.Method)
		}
		return nil
	case ScopeHubBlock:
		return v.authorizeHubBlockLocked(cmd, now)
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrScope, d.Scope)
	}
}

// authorizeHubBlockLocked is the WhatsApp key: one action, on one device,
// never a protected one, and not too often.
func (v *Verifier) authorizeHubBlockLocked(cmd Command, now time.Time) error {
	mac, ok := BlockTargetMAC(cmd.Path)
	if !ok || cmd.Method != "POST" || !strings.HasSuffix(cmd.Path, "/block") {
		return fmt.Errorf("%w: hub_block may only block a device", ErrScope)
	}
	// Protected covers both the owner's list and what the host added, which
	// is how the relay keeps the narrow key off the device carrying it.
	if v.bundle.IsProtected(mac) || v.protected[normaliseMAC(mac)] {
		return fmt.Errorf("%w: %s", ErrProtected, mac)
	}
	recent := 0
	for _, t := range v.hubBlocks {
		if now.Sub(t) < v.HubBlockWindow {
			recent++
		}
	}
	if recent >= v.HubBlockLimit {
		return fmt.Errorf("%w: %d blocks in the last %s", ErrRateLimited, recent, v.HubBlockWindow)
	}
	return nil
}

// freshLocked checks the command's lifetime.
func (v *Verifier) freshLocked(cmd Command, now time.Time) error {
	if cmd.IssuedAt.After(now.Add(v.ClockSkew)) {
		return fmt.Errorf("%w: issued at %s", ErrNotYetValid, cmd.IssuedAt)
	}
	if cmd.ExpiresAt.Sub(cmd.IssuedAt) > MaxCommandTTL {
		return fmt.Errorf("%w: %s", ErrTooLongLived, cmd.ExpiresAt.Sub(cmd.IssuedAt))
	}
	if !now.Before(cmd.ExpiresAt) {
		return fmt.Errorf("%w: expired at %s", ErrExpired, cmd.ExpiresAt)
	}
	// Anything issued before this relay started cannot be fresh, and we have
	// no memory of it: refuse rather than risk replaying across a restart.
	if cmd.IssuedAt.Before(v.startedAt) {
		return fmt.Errorf("%w: issued before the relay started", ErrExpired)
	}
	return nil
}

// IsHubBlock reports whether a key is the hub's narrow WhatsApp key. The
// relay asks before it runs the command: that key may only reach a device
// the relay has itself just seen on the network, so the hub cannot name a
// device it made up.
func (v *Verifier) IsHubBlock(keyID string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bundle == nil {
		return false
	}
	return v.isHubBlockLocked(keyID)
}

func (v *Verifier) isHubBlockLocked(keyID string) bool {
	d, ok := v.bundle.delegation(keyID)
	return ok && d.Scope == ScopeHubBlock
}

// purgeLocked drops replay entries that can no longer be replayed anyway.
func (v *Verifier) purgeLocked(now time.Time) {
	for id, exp := range v.seen {
		if !now.Before(exp) {
			delete(v.seen, id)
		}
	}
	kept := v.hubBlocks[:0]
	for _, t := range v.hubBlocks {
		if now.Sub(t) < v.HubBlockWindow {
			kept = append(kept, t)
		}
	}
	v.hubBlocks = kept
}

// VerifyResult checks a relay's answer: signed by a relay this bundle knows,
// for this household and this command. The app and the hub both use it, so
// neither has to take the hub's word for an outcome.
func (v *Verifier) VerifyResult(sr SignedResult, cmdID string) (Result, error) {
	claimed, err := sr.Unverified()
	if err != nil {
		return Result{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bundle == nil {
		return Result{}, ErrNoBundle
	}
	relay, ok := v.bundle.RelayByKeyID(claimed.KeyID)
	if !ok || v.bundle.isRevoked(claimed.KeyID) {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownKey, claimed.KeyID)
	}
	res, err := sr.Open(DomainResult, relay.Keys.signPub())
	if err != nil {
		return Result{}, err
	}
	if res.TenantID != v.tenantID {
		return Result{}, ErrTenant
	}
	if cmdID != "" && res.CmdID != cmdID {
		return Result{}, fmt.Errorf("%w: result is for command %s", ErrMalformed, res.CmdID)
	}
	return res, nil
}

// VerifySnapshot checks state a relay pushed, the same way.
func (v *Verifier) VerifySnapshot(ss SignedSnapshot) (Snapshot, error) {
	claimed, err := ss.Unverified()
	if err != nil {
		return Snapshot{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.bundle == nil {
		return Snapshot{}, ErrNoBundle
	}
	relay, ok := v.bundle.RelayByKeyID(claimed.KeyID)
	if !ok || v.bundle.isRevoked(claimed.KeyID) {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrUnknownKey, claimed.KeyID)
	}
	snap, err := ss.Open(DomainSnapshot, relay.Keys.signPub())
	if err != nil {
		return Snapshot{}, err
	}
	if snap.TenantID != v.tenantID {
		return Snapshot{}, ErrTenant
	}
	return snap, nil
}
