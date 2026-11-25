package domain

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
)

func TestGrantAndRevokeLifetime(t *testing.T) {
	paid := Subscription{Account: "a", BoxID: "BOX1", Tier: TierBasic, ExpiresAt: t0.Add(10 * day)}
	life, err := paid.GrantLifetime(TierMax)
	if err != nil {
		t.Fatal(err)
	}
	if !life.Lifetime || life.Tier != TierMax || !life.ExpiresAt.Equal(LifetimeExpiresAt) {
		t.Fatalf("granted = %+v", life)
	}
	if !life.Active(t0.AddDate(50, 0, 0)) {
		t.Error("a lifetime pass ended")
	}
	if _, ok := life.ReminderStepAt(t0); ok {
		t.Error("a lifetime pass is due a reminder")
	}
	if _, ok := life.ReminderStepAt(LifetimeExpiresAt.Add(-day)); ok {
		t.Error("a lifetime pass is due a reminder")
	}
	if _, err := life.Extend(5, t0); !errors.Is(err, ErrLifetimePass) {
		t.Errorf("Extend = %v, want ErrLifetimePass", err)
	}
	if err := life.CanBuy(mustPass(t, TierMax, Term1Month), t0); !errors.Is(err, ErrLifetimePass) {
		t.Errorf("CanBuy = %v, want ErrLifetimePass", err)
	}

	// Granting again changes the tier only; what ran before stays aside.
	again, err := life.GrantLifetime(TierBasic)
	if err != nil || again.Tier != TierBasic || again.PreLifetimeTier != TierBasic || !again.PreLifetimeExpiresAt.Equal(paid.ExpiresAt) {
		t.Fatalf("granted again = %+v, %v", again, err)
	}

	// A payment arriving anyway goes to the pass kept aside.
	credited, err := life.Credit(mustPass(t, TierBasic, Term1Month), t0)
	if err != nil {
		t.Fatal(err)
	}
	if !credited.Lifetime || !credited.ExpiresAt.Equal(LifetimeExpiresAt) || !credited.PreLifetimeExpiresAt.Equal(t0.Add(40*day)) {
		t.Fatalf("credited = %+v", credited)
	}

	// Revoked while the kept pass still runs: it comes back, paid days included.
	back, err := credited.RevokeLifetime(t0)
	if err != nil {
		t.Fatal(err)
	}
	if back.Lifetime || back.Tier != TierBasic || !back.ExpiresAt.Equal(t0.Add(40*day)) || back.PreLifetimeTier != "" {
		t.Fatalf("revoked = %+v", back)
	}
	// Revoked after it would have ended: the box is Free from now.
	late := t0.Add(60 * day)
	ended, err := credited.RevokeLifetime(late)
	if err != nil || ended.Lifetime || ended.Active(late) || !ended.ExpiresAt.Equal(late) || ended.Tier != TierMax {
		t.Fatalf("revoked late = %+v, %v", ended, err)
	}
	if _, err := back.RevokeLifetime(t0); !errors.Is(err, ErrNotLifetime) {
		t.Errorf("revoke twice = %v, want ErrNotLifetime", err)
	}

	// A box that never had a pass: nothing comes back.
	fresh, _ := Subscription{Account: "a", BoxID: "BOX2"}.GrantLifetime(TierBasic)
	if gone, _ := fresh.RevokeLifetime(t0); gone.Active(t0) {
		t.Errorf("revoked fresh = %+v", gone)
	}
	if _, err := (Subscription{}).GrantLifetime(TierFree); !errors.Is(err, ErrUnknownPass) {
		t.Errorf("free lifetime = %v", err)
	}
}

// TestLifetimePlay: Play keeps billing a box given a lifetime pass; its
// renewals and its end touch the pass kept aside only.
func TestLifetimePlay(t *testing.T) {
	life, _ := Subscription{Tier: TierBasic, ExpiresAt: t0.Add(day), Channel: ChannelPlay}.GrantLifetime(TierMax)
	renewed, err := life.ApplyPlay(TierBasic, t0.Add(31*day))
	if err != nil || !renewed.Lifetime || renewed.Tier != TierMax || !renewed.PreLifetimeExpiresAt.Equal(t0.Add(31*day)) {
		t.Fatalf("ApplyPlay = %+v, %v", renewed, err)
	}
	ended := renewed.EndPlay(t0)
	if !ended.Lifetime || !ended.ExpiresAt.Equal(LifetimeExpiresAt) || !ended.PreLifetimeExpiresAt.Equal(t0) {
		t.Fatalf("EndPlay = %+v", ended)
	}
}

func TestManualPass(t *testing.T) {
	p, err := ManualPass(TierMax, 30)
	if err != nil || p.Code != "max_30d" || p.Days != 30 || p.Tier != TierMax {
		t.Fatalf("ManualPass = %+v, %v", p, err)
	}
	if back, err := ManualPassByCode("max_30d"); err != nil || back != p {
		t.Errorf("ManualPassByCode = %+v, %v", back, err)
	}
	for _, bad := range []string{"max_1m", "max_0d", "free_30d", "max_366d", "max_030d", "max_d", "max30d", ""} {
		if _, err := ManualPassByCode(bad); err == nil {
			t.Errorf("ManualPassByCode(%q) accepted", bad)
		}
	}
	// Not a catalogue code: no checkout can sell it.
	if _, err := PassByCode(p.Code); err == nil {
		t.Error("a manual pass is in the catalogue")
	}
	if err := (Subscription{}).CanBuy(p, t0); !errors.Is(err, ErrUnknownPass) {
		t.Errorf("CanBuy(manual) = %v", err)
	}
	// Renewing by hand extends from the current end, as any pass does.
	sub, err := Subscription{Tier: TierMax, ExpiresAt: t0.Add(5 * day)}.Credit(p, t0)
	if err != nil || !sub.ExpiresAt.Equal(t0.Add(35*day)) {
		t.Errorf("Credit(manual) = %+v, %v", sub, err)
	}
}

// TestLifetimeToken is the token of a lifetime pass: the usual one, capped
// at TokenMaxLifetime and refreshed, so the format the installed apps
// verify (internal/entitlement, mobile/src/licensing/token.ts) is unchanged
// and it verifies there as any other.
func TestLifetimeToken(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	life, _ := Subscription{Account: "acc-1", BoxID: "ZTEG1A2B3C4D"}.GrantLifetime(TierMax)
	now := t0.AddDate(30, 0, 0) // decades later: still entitled
	c, err := IssueClaims(life, now)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ExpiresAt.Equal(now.Add(TokenMaxLifetime)) {
		t.Fatalf("token expiry = %v, want capped at %v", c.ExpiresAt, now.Add(TokenMaxLifetime))
	}
	tok, err := Sign(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := entitlement.NewKeySet(pub)
	if err != nil {
		t.Fatal(err)
	}
	got, inGrace, err := entitlement.Verify(keys, tok, now.Add(time.Hour))
	if err != nil || inGrace || got.Tier != TierMax || got.BoxID != "ZTEG1A2B3C4D" {
		t.Fatalf("Verify = %+v, %v, %v", got, inGrace, err)
	}
	if err := entitlement.Authorize(keys, tok, "ZTEG1A2B3C4D", entitlement.FeatureSSIDManage, now.Add(time.Hour)); err != nil {
		t.Errorf("Authorize = %v", err)
	}
	// The far end encodes: the hub's JSON views carry it as is.
	if _, err := json.Marshal(life.ExpiresAt); err != nil {
		t.Errorf("LifetimeExpiresAt does not encode: %v", err)
	}
}
