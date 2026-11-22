package handler

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The hub's second opinion on a member's command (issue #47). The relay
// decides; the hub refuses early what the relay would certainly refuse, so
// the member reads a reason instead of waiting a minute for "expired".

const policyTenant = "11111111-1111-1111-1111-111111111111"

var policyNow = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

type staticTrust struct{ b agentsdomain.TrustBundle }

func (s staticTrust) GetBundle(context.Context, string) (agentsdomain.TrustBundle, error) {
	return s.b, nil
}

func gen(t *testing.T) *trust.Identity {
	t.Helper()
	id, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func policyFor(t *testing.T, owner *trust.Identity, b trust.Bundle, now time.Time) *Policy {
	t.Helper()
	s, err := trust.Sign(owner, trust.DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	return &Policy{
		Trust: staticTrust{agentsdomain.TrustBundle{TenantID: policyTenant, Version: b.Version, Payload: s.Payload, Sig: s.Sig}},
		Now:   func() time.Time { return now },
	}
}

func memberBlock(t *testing.T, member *trust.Identity, at time.Time) trust.SignedCommand {
	t.Helper()
	sc, err := trust.NewCommand(member, policyTenant, "POST", "/api/devices/ac:bb:cc:00:00:22/block", nil, at, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func TestCheckCommand_MemberDelegations(t *testing.T) {
	owner, member := gen(t), gen(t)
	admin := tenantaccess.Caller{UserID: "u-2", TenantID: policyTenant, Role: memberdomain.MEMBER_ROLE_ADMIN}
	base := func() trust.Bundle {
		b := trust.Bundle{TenantID: policyTenant, Version: 3, Owner: owner.Public()}
		if err := b.Delegate(member.Public(), trust.ScopeControl, "Awa", policyNow); err != nil {
			t.Fatal(err)
		}
		return b
	}

	if err := policyFor(t, owner, base(), policyNow).CheckCommand(context.Background(), admin, memberBlock(t, member, policyNow)); err != nil {
		t.Fatalf("a control member's block: %v", err)
	}

	later := policyNow.Add(trust.DelegationTTL + time.Hour)
	if err := policyFor(t, owner, base(), later).CheckCommand(context.Background(), admin, memberBlock(t, member, later)); !errors.Is(err, trust.ErrDelegationExpired) {
		t.Fatalf("an expired delegation = %v, want ErrDelegationExpired", err)
	}

	revoked := base()
	revoked.Revoke(member.KeyID())
	if err := policyFor(t, owner, revoked, policyNow).CheckCommand(context.Background(), admin, memberBlock(t, member, policyNow)); !errors.Is(err, trust.ErrRevoked) {
		t.Fatalf("a revoked delegation = %v, want ErrRevoked", err)
	}

	ownerOnly := base()
	ownerOnly.OwnerOnly = true
	if err := policyFor(t, owner, ownerOnly, policyNow).CheckCommand(context.Background(), admin, memberBlock(t, member, policyNow)); !errors.Is(err, trust.ErrOwnerOnly) {
		t.Fatalf("an owner-only household = %v, want ErrOwnerOnly", err)
	}

	view := base()
	_ = view.Delegate(member.Public(), trust.ScopeView, "Awa", policyNow)
	if err := policyFor(t, owner, view, policyNow).CheckCommand(context.Background(), admin, memberBlock(t, member, policyNow)); !errors.Is(err, trust.ErrScope) {
		t.Fatalf("a view member's block = %v, want ErrScope", err)
	}
}
