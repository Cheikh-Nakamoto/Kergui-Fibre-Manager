package trust

import (
	"errors"
	"testing"
	"time"
)

func signPolicy(t *testing.T, id *Identity, p Policy) Signed[Policy] {
	t.Helper()
	sp, err := Sign(id, DomainPolicy, p)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func testPolicy(version uint64) Policy {
	return Policy{
		TenantID: tenant, Version: version, IssuedAt: now,
		AutoReject: AutoRejectPolicy{
			Enabled: true, Since: now.Add(-time.Hour),
			Accepted: []string{"AA:BB:CC:00:00:01"},
		},
		Entitlement: "payload.sig",
	}
}

func TestAcceptPolicy(t *testing.T) {
	h := newHousehold(t)
	p, err := h.v.AcceptPolicy(signPolicy(t, h.owner, testPolicy(1)))
	if err != nil {
		t.Fatalf("AcceptPolicy: %v", err)
	}
	if !p.AutoReject.Enabled || p.Entitlement != "payload.sig" || len(p.AutoReject.Accepted) != 1 {
		t.Fatalf("policy = %+v", p)
	}
	if got := h.v.Policy(); got == nil || got.Version != 1 {
		t.Fatalf("Policy() = %+v, want version 1", got)
	}
}

// The hub carries the policy; it must not be able to write one. A policy
// turned on by the hub would have the relay cut the household's devices.
func TestAcceptPolicy_Refusals(t *testing.T) {
	cases := []struct {
		name string
		sign func(h household) Signed[Policy]
		want error
	}{
		{"signed by the hub", func(h household) Signed[Policy] { return signPolicy(t, h.hub, testPolicy(1)) }, ErrBadSignature},
		{"signed by a member", func(h household) Signed[Policy] { return signPolicy(t, h.member, testPolicy(1)) }, ErrBadSignature},
		{"unsigned", func(h household) Signed[Policy] {
			sp := signPolicy(t, h.owner, testPolicy(1))
			sp.Sig = nil
			return sp
		}, ErrUnsigned},
		{"another household", func(h household) Signed[Policy] {
			p := testPolicy(1)
			p.TenantID = "22222222-2222-2222-2222-222222222222"
			return signPolicy(t, h.owner, p)
		}, ErrTenant},
		{"no version", func(h household) Signed[Policy] { return signPolicy(t, h.owner, testPolicy(0)) }, ErrMalformed},
		{"a bundle replayed as a policy", func(h household) Signed[Policy] {
			sb, err := Sign(h.owner, DomainBundle, testPolicy(1))
			if err != nil {
				t.Fatal(err)
			}
			return Signed[Policy]{Payload: sb.Payload, Sig: sb.Sig}
		}, ErrBadSignature},
	}
	for _, c := range cases {
		h := newHousehold(t)
		if _, err := h.v.AcceptPolicy(c.sign(h)); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
		if h.v.Policy() != nil {
			t.Errorf("%s: a refused policy was installed", c.name)
		}
	}
}

// A hub replaying an older policy — one with a pass that has since been
// refreshed, or with the rule still on after the owner turned it off — is
// refused.
func TestAcceptPolicy_NoRollback(t *testing.T) {
	h := newHousehold(t)
	if _, err := h.v.AcceptPolicy(signPolicy(t, h.owner, testPolicy(2))); err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint64{1, 2} {
		if _, err := h.v.AcceptPolicy(signPolicy(t, h.owner, testPolicy(v))); !errors.Is(err, ErrStalePolicy) {
			t.Errorf("version %d after 2: err = %v, want ErrStalePolicy", v, err)
		}
	}
	if _, err := h.v.AcceptPolicy(signPolicy(t, h.owner, testPolicy(3))); err != nil {
		t.Fatalf("version 3: %v", err)
	}
}
