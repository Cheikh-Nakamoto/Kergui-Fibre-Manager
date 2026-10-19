package trust

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

// The clock is fixed: every lifetime in this package is relative, so a test
// that pins "now" reads as the specification does.
var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const tenant = "11111111-1111-1111-1111-111111111111"

type household struct {
	owner, member, hub, relay *Identity
	v                         *Verifier
}

func mustID(t *testing.T) *Identity {
	t.Helper()
	id, err := Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// newHousehold builds an owner, a relay, a member delegated control and the
// hub's narrow block key, and installs the bundle in a verifier that has been
// up for a minute.
func newHousehold(t *testing.T, mutate ...func(*Bundle)) household {
	t.Helper()
	return newHouseholdStartedAt(t, now.Add(-time.Minute), mutate...)
}

func newHouseholdStartedAt(t *testing.T, startedAt time.Time, mutate ...func(*Bundle)) household {
	t.Helper()
	h := household{owner: mustID(t), member: mustID(t), hub: mustID(t), relay: mustID(t)}
	b := Bundle{
		TenantID: tenant,
		Version:  1,
		IssuedAt: now,
		Owner:    h.owner.Public(),
		Relays: []Relay{
			{AgentID: "agent-1", Kind: "pc", Keys: h.relay.Public()},
		},
		Delegations: []Delegation{
			{Keys: h.member.Public(), Scope: ScopeControl, ExpiresAt: now.Add(30 * 24 * time.Hour)},
			{Keys: h.hub.Public(), Scope: ScopeHubBlock, ExpiresAt: now.Add(30 * 24 * time.Hour)},
		},
		ProtectedMACs: []string{"AA:BB:CC:00:00:01"},
	}
	for _, m := range mutate {
		m(&b)
	}
	h.v = NewVerifier(tenant, h.owner.Public().signPub(), startedAt)
	sb, err := Sign(h.owner, DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.v.AcceptBundle(sb); err != nil {
		t.Fatalf("AcceptBundle: %v", err)
	}
	return h
}

func cmd(t *testing.T, id *Identity, method, path string) SignedCommand {
	t.Helper()
	sc, err := NewCommand(id, tenant, method, path, nil, now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

// resign rebuilds a command with arbitrary fields, for the cases a
// well-behaved NewCommand would refuse to produce.
func resign(t *testing.T, id *Identity, c Command) SignedCommand {
	t.Helper()
	c.KeyID = id.KeyID()
	sc, err := Sign(id, DomainCommand, c)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func base(t *testing.T, id *Identity) Command {
	t.Helper()
	sc := cmd(t, id, "POST", "/api/devices/ac:bb:cc:00:00:22/block")
	c, err := sc.Unverified()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerifyCommand_OwnerIsAccepted(t *testing.T) {
	h := newHousehold(t)
	got, err := h.v.VerifyCommand(cmd(t, h.owner, "POST", "/api/devices/ac:bb:cc:00:00:22/block"), nil, now)
	if err != nil {
		t.Fatalf("owner command refused: %v", err)
	}
	if got.KeyID != h.owner.KeyID() {
		t.Errorf("KeyID = %s, want the owner's", got.KeyID)
	}
}

func TestVerifyCommand_ControlDelegationIsAccepted(t *testing.T) {
	h := newHousehold(t)
	if _, err := h.v.VerifyCommand(cmd(t, h.member, "POST", "/api/devices/ac:bb:cc:00:00:22/block"), nil, now); err != nil {
		t.Fatalf("delegated control refused: %v", err)
	}
}

func TestVerifyCommand_BodyIsCommitted(t *testing.T) {
	h := newHousehold(t)
	body := []byte(`{"enabled":true}`)
	sc, err := NewCommand(h.owner, tenant, "POST", "/api/filter/toggle", body, now, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.VerifyCommand(sc, body, now); err != nil {
		t.Fatalf("matching body refused: %v", err)
	}
}

// TestVerifyCommand_Refusals walks the Vérification section of RFC-005: every
// way a hub, a member or a replay could try to widen what reaches the box.
func TestVerifyCommand_Refusals(t *testing.T) {
	tests := []struct {
		name      string
		build     func(t *testing.T, h household) (SignedCommand, []byte)
		at        time.Time // when the relay verifies; defaults to now
		startedAt time.Time // when the relay came up; defaults to a minute ago
		mutate    func(*Bundle)
		wantErr   error
	}{
		{
			name: "the hub forwards an unsigned command",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				sc := cmd(t, h.owner, "POST", "/api/devices/ac:bb:cc:00:00:22/block")
				sc.Sig = nil
				return sc, nil
			},
			wantErr: ErrUnsigned,
		},
		{
			name: "the hub keeps the signature but edits the path",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				sc := cmd(t, h.owner, "GET", "/api/devices")
				sc.Payload = []byte(string(sc.Payload)) // same bytes, then tamper
				c, _ := sc.Unverified()
				c.Path = "/api/devices/ac:bb:cc:00:00:22/block"
				tampered, _ := Sign(h.hub, DomainCommand, c) // hub signs with its own key
				tampered.Sig = sc.Sig                        // ... but replays the owner's signature
				return tampered, nil
			},
			wantErr: ErrBadSignature,
		},
		{
			name: "a key that was never delegated",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				stranger := mustID(t)
				return cmd(t, stranger, "GET", "/api/devices"), nil
			},
			wantErr: ErrUnknownKey,
		},
		{
			name: "a view delegation tries to block",
			mutate: func(b *Bundle) {
				b.Delegations[0].Scope = ScopeView
			},
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.member, "POST", "/api/devices/ac:bb:cc:00:00:22/block"), nil
			},
			wantErr: ErrScope,
		},
		{
			name:   "owner_only, so a delegated key may not act",
			mutate: func(b *Bundle) { b.OwnerOnly = true },
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.member, "GET", "/api/devices"), nil
			},
			wantErr: ErrOwnerOnly,
		},
		{
			name: "an expired delegation",
			mutate: func(b *Bundle) {
				b.Delegations[0].ExpiresAt = now.Add(-time.Second)
			},
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.member, "GET", "/api/devices"), nil
			},
			wantErr: ErrDelegationExpired,
		},
		{
			name: "an expired command",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.owner, "GET", "/api/devices"), nil
			},
			at:      now.Add(31 * time.Second),
			wantErr: ErrExpired,
		},
		{
			name: "a command that would outlive the 60 s maximum",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				c := base(t, h.owner)
				c.ExpiresAt = c.IssuedAt.Add(2 * time.Hour)
				return resign(t, h.owner, c), nil
			},
			wantErr: ErrTooLongLived,
		},
		{
			name: "a command issued in the future",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				c := base(t, h.owner)
				c.IssuedAt = now.Add(time.Hour)
				c.ExpiresAt = c.IssuedAt.Add(30 * time.Second)
				return resign(t, h.owner, c), nil
			},
			wantErr: ErrNotYetValid,
		},
		{
			// A relay that just came up has no memory of commands issued
			// before it, so it cannot tell a fresh one from a replay. Past
			// the first minute this case is unreachable anyway: with a 60 s
			// cap, anything issued before start-up has already expired.
			name:      "a command issued before the relay started",
			startedAt: now.Add(-10 * time.Second),
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				c := base(t, h.owner)
				c.IssuedAt = now.Add(-20 * time.Second)
				c.ExpiresAt = now.Add(10 * time.Second)
				return resign(t, h.owner, c), nil
			},
			wantErr: ErrExpired,
		},
		{
			name: "a revoked key",
			mutate: func(b *Bundle) {
				b.Revoked = []string{b.Delegations[0].KeyID()}
			},
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.member, "GET", "/api/devices"), nil
			},
			wantErr: ErrRevoked,
		},
		{
			name: "a command for another household",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				sc, err := NewCommand(h.owner, "22222222-2222-2222-2222-222222222222", "GET", "/api/devices", nil, now, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				return sc, nil
			},
			wantErr: ErrTenant,
		},
		{
			name: "the body was swapped in flight",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				sc, err := NewCommand(h.owner, tenant, "POST", "/api/filter/toggle", []byte(`{"enabled":true}`), now, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				return sc, []byte(`{"enabled":false}`)
			},
			wantErr: ErrBodyMismatch,
		},
		{
			name: "the hub key tries to unblock",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.hub, "POST", "/api/devices/ac:bb:cc:00:00:22/unblock"), nil
			},
			wantErr: ErrScope,
		},
		{
			name: "the hub key tries to read the device list",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.hub, "GET", "/api/devices"), nil
			},
			wantErr: ErrScope,
		},
		{
			name: "the hub key tries to block a protected device",
			build: func(t *testing.T, h household) (SignedCommand, []byte) {
				return cmd(t, h.hub, "POST", "/api/devices/aa:bb:cc:00:00:01/block"), nil
			},
			wantErr: ErrProtected,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			startedAt := tc.startedAt
			if startedAt.IsZero() {
				startedAt = now.Add(-time.Minute)
			}
			var mutate []func(*Bundle)
			if tc.mutate != nil {
				mutate = append(mutate, tc.mutate)
			}
			h := newHouseholdStartedAt(t, startedAt, mutate...)
			at := tc.at
			if at.IsZero() {
				at = now
			}
			sc, body := tc.build(t, h)
			_, err := h.v.VerifyCommand(sc, body, at)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyCommand_ReplayIsRefusedOnce(t *testing.T) {
	h := newHousehold(t)
	sc := cmd(t, h.owner, "POST", "/api/devices/ac:bb:cc:00:00:22/block")
	if _, err := h.v.VerifyCommand(sc, nil, now); err != nil {
		t.Fatalf("first run refused: %v", err)
	}
	if _, err := h.v.VerifyCommand(sc, nil, now.Add(time.Second)); !errors.Is(err, ErrReplay) {
		t.Fatalf("second run: err = %v, want ErrReplay", err)
	}
}

func TestVerifyCommand_RelayProtectsItself(t *testing.T) {
	h := newHousehold(t)
	h.v.Protect("AC:BB:CC:00:00:22") // the MAC the agent found for its own LAN IP
	if _, err := h.v.VerifyCommand(cmd(t, h.hub, "POST", "/api/devices/ac:bb:cc:00:00:22/block"), nil, now); !errors.Is(err, ErrProtected) {
		t.Fatalf("err = %v, want ErrProtected", err)
	}
}

func TestVerifyCommand_HubKeyIsRateLimited(t *testing.T) {
	h := newHousehold(t)
	h.v.HubBlockLimit = 2
	for i := range 2 {
		if _, err := h.v.VerifyCommand(cmd(t, h.hub, "POST", "/api/devices/ac:bb:cc:00:00:22/block"), nil, now); err != nil {
			t.Fatalf("block %d refused: %v", i, err)
		}
	}
	if _, err := h.v.VerifyCommand(cmd(t, h.hub, "POST", "/api/devices/ac:bb:cc:00:00:22/block"), nil, now); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	// The window slides: the same key works again later.
	later := now.Add(2 * time.Hour)
	sc, err := NewCommand(h.hub, tenant, "POST", "/api/devices/ac:bb:cc:00:00:22/block", nil, later, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.VerifyCommand(sc, nil, later); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestAcceptBundle_Refusals(t *testing.T) {
	h := newHousehold(t)
	stranger := mustID(t)

	newer := Bundle{TenantID: tenant, Version: 2, IssuedAt: now, Owner: h.owner.Public()}

	t.Run("signed by someone other than the owner", func(t *testing.T) {
		sb, err := Sign(stranger, DomainBundle, newer)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.v.AcceptBundle(sb); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("err = %v, want ErrBadSignature", err)
		}
	})

	t.Run("an older version is a rollback", func(t *testing.T) {
		old := newer
		old.Version = 1
		sb, err := Sign(h.owner, DomainBundle, old)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.v.AcceptBundle(sb); !errors.Is(err, ErrStaleBundle) {
			t.Fatalf("err = %v, want ErrStaleBundle", err)
		}
	})

	t.Run("another household", func(t *testing.T) {
		other := newer
		other.TenantID = "33333333-3333-3333-3333-333333333333"
		sb, err := Sign(h.owner, DomainBundle, other)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.v.AcceptBundle(sb); !errors.Is(err, ErrTenant) {
			t.Fatalf("err = %v, want ErrTenant", err)
		}
	})

	t.Run("a bundle that renames the owner", func(t *testing.T) {
		swapped := newer
		swapped.Owner = stranger.Public()
		sb, err := Sign(h.owner, DomainBundle, swapped)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.v.AcceptBundle(sb); !errors.Is(err, ErrUnknownKey) {
			t.Fatalf("err = %v, want ErrUnknownKey", err)
		}
	})

	t.Run("a newer version is installed", func(t *testing.T) {
		sb, err := Sign(h.owner, DomainBundle, newer)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.v.AcceptBundle(sb); err != nil {
			t.Fatalf("AcceptBundle: %v", err)
		}
		if h.v.Bundle().Version != 2 {
			t.Errorf("version = %d, want 2", h.v.Bundle().Version)
		}
	})
}

func TestVerifyCommand_NeedsABundle(t *testing.T) {
	owner := mustID(t)
	v := NewVerifier(tenant, owner.Public().signPub(), now)
	if _, err := v.VerifyCommand(cmd(t, owner, "GET", "/api/devices"), nil, now); !errors.Is(err, ErrNoBundle) {
		t.Fatalf("err = %v, want ErrNoBundle", err)
	}
}

func TestVerifyResultAndSnapshot(t *testing.T) {
	h := newHousehold(t)

	sr, err := NewResult(h.relay, tenant, "cmd-1", 7, 200, []byte(`{"status":"blocked"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.v.VerifyResult(sr, "cmd-1")
	if err != nil {
		t.Fatalf("VerifyResult: %v", err)
	}
	if res.Status != 200 || res.Epoch != 7 {
		t.Errorf("result = %+v", res)
	}

	// A hub inventing a result signs with a key no relay owns.
	forged, err := NewResult(h.hub, tenant, "cmd-1", 7, 200, []byte(`{"status":"blocked"}`), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.v.VerifyResult(forged, "cmd-1"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("forged result: err = %v, want ErrUnknownKey", err)
	}
	// Nor can a real result be re-labelled as the answer to another command.
	if _, err := h.v.VerifyResult(sr, "cmd-2"); err == nil {
		t.Fatal("a result was accepted for the wrong command id")
	}

	ss, err := NewSnapshot(h.relay, tenant, "devices", "agent-1", 7, []byte(`[{"MAC":"ac:bb:cc:00:00:22"}]`), now)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := h.v.VerifySnapshot(ss)
	if err != nil {
		t.Fatalf("VerifySnapshot: %v", err)
	}
	if snap.Kind != "devices" || snap.AgentID != "agent-1" {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestSignedPayloadIsNotReEncoded(t *testing.T) {
	// A verifier must check the bytes it received, not a re-serialisation:
	// a field added by a newer signer must not break the signature.
	h := newHousehold(t)
	sc := cmd(t, h.owner, "GET", "/api/devices")
	c, _ := sc.Unverified()
	withExtra := append([]byte(nil), sc.Payload[:len(sc.Payload)-1]...)
	withExtra = append(withExtra, []byte(`,"future_field":42}`)...)

	// Re-sign the extended payload and check it still verifies and decodes.
	extended := Signed[Command]{Payload: withExtra}
	extended.Sig = signWith(t, h.owner, DomainCommand, withExtra)
	got, err := h.v.VerifyCommand(extended, nil, now)
	if err != nil {
		t.Fatalf("a payload with an unknown field was refused: %v", err)
	}
	if got.CmdID != c.CmdID {
		t.Errorf("CmdID = %s, want %s", got.CmdID, c.CmdID)
	}
}

func signWith(t *testing.T, id *Identity, domain string, payload []byte) []byte {
	t.Helper()
	return ed25519.Sign(id.signKey, signingInput(domain, payload))
}

func TestBlockTargetMAC(t *testing.T) {
	for _, tc := range []struct {
		path, mac string
		ok        bool
	}{
		{"/api/devices/ac:bb:cc:00:00:22/block", "ac:bb:cc:00:00:22", true},
		{"/api/devices/ac:bb:cc:00:00:22/wifi-unblock", "ac:bb:cc:00:00:22", true},
		{"/api/devices", "", false},
		{"/api/devices/ac:bb:cc:00:00:22", "", false},
		{"/api/login", "", false},
		{"/api/devices/x/block/extra", "", false},
	} {
		mac, ok := BlockTargetMAC(tc.path)
		if ok != tc.ok || mac != tc.mac {
			t.Errorf("BlockTargetMAC(%q) = (%q, %v), want (%q, %v)", tc.path, mac, ok, tc.mac, tc.ok)
		}
	}
}
