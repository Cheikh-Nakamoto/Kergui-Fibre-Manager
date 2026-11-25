package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

func TestFindAccount_ByTheNumberTypedAnyWay(t *testing.T) {
	f := newFixture(t)
	acc := f.signIn(t)
	for _, raw := range []string{"771234567", "77 123 45 67", "+221771234567", "00221771234567", "+221 77-123-45-67"} {
		got, err := f.svc.FindAccount(context.Background(), raw)
		if err != nil || got.ID != acc.ID {
			t.Fatalf("FindAccount(%q) = %+v %v", raw, got, err)
		}
	}
	if _, err := f.svc.FindAccount(context.Background(), "781234567"); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("an unknown number: %v", err)
	}
	if _, err := f.svc.FindAccount(context.Background(), "pas un numéro"); err == nil {
		t.Fatal("a bad number was accepted")
	}
	// Looking someone up sends them nothing: no sign-in code from support.
	if n := f.sender.count(phone); n != 1 {
		t.Fatalf("%d messages to the number, want only its own sign-in code", n)
	}
}

func TestExtendPass_MovesTheEndDate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "r1"}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Subscription(ctx, acc.ID, box)
	sub, err := f.svc.ExtendPass(ctx, acc.ID, "zteg00000001", 5)
	if err != nil || !sub.ExpiresAt.Equal(before.ExpiresAt.AddDate(0, 0, 5)) || sub.Tier != domain.TierMax {
		t.Fatalf("extend = %+v %v (was %v)", sub, err, before.ExpiresAt)
	}
	if stored, _ := f.store.Subscription(ctx, acc.ID, box); !stored.ExpiresAt.Equal(sub.ExpiresAt) {
		t.Fatalf("stored %v, want %v", stored.ExpiresAt, sub.ExpiresAt)
	}
	if _, err := f.svc.ExtendPass(ctx, acc.ID, box2, 5); !errors.Is(err, domain.ErrNoActivePass) {
		t.Fatalf("a box without a pass: %v", err)
	}
	if _, err := f.svc.ExtendPass(ctx, acc.ID, box, 366); !errors.Is(err, domain.ErrExtendDays) {
		t.Fatalf("366 days: %v", err)
	}
}

func TestAdminTransfer_PastTheCustomerLimit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_3m", Provider: usecase.ProviderNabooPay, Reference: "r1"}); err != nil {
		t.Fatal(err)
	}
	for _, mv := range [][2]string{{box, box2}, {box2, box}} {
		if _, err := f.svc.Transfer(ctx, acc.ID, mv[0], mv[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Transfer(ctx, acc.ID, box, box2); !errors.Is(err, domain.ErrTransferLimit) {
		t.Fatalf("the customer's third transfer: %v", err)
	}
	before, _ := f.store.Subscription(ctx, acc.ID, box)
	sub, err := f.svc.AdminTransfer(ctx, acc.ID, box, box2)
	if err != nil || sub.BoxID != box2 || !sub.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("admin transfer = %+v %v", sub, err)
	}
	// It does not use up the customer's own transfers.
	if n := len(f.store.Transfers()); n != 2 {
		t.Fatalf("%d transfers counted against the customer, want 2", n)
	}
	// The other rules hold: nothing to move, the same box.
	if _, err := f.svc.AdminTransfer(ctx, acc.ID, box, box2); !errors.Is(err, domain.ErrNoActivePass) {
		t.Fatalf("nothing on the old box: %v", err)
	}
	if _, err := f.svc.AdminTransfer(ctx, acc.ID, box2, box2); !errors.Is(err, domain.ErrSameBox) {
		t.Fatalf("same box: %v", err)
	}
}

func TestRevokeSessions_SignsEveryPhoneOutOfTheAppAndTheHub(t *testing.T) {
	f := newFixture(t)
	id := &fakeIdentity{}
	f.svc.Identity = id
	ctx := context.Background()
	var tokens []string
	for i, device := range []string{"Pixel", "Tecno"} {
		if i > 0 {
			f.advance(2 * time.Minute)
		}
		if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
			t.Fatal(err)
		}
		in, err := f.svc.VerifyOTP(ctx, phone, f.sender.code(t), device)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, in.Token)
	}
	acc, _ := f.svc.Authenticate(ctx, tokens[0])
	n, err := f.svc.RevokeSessions(ctx, acc.ID)
	if err != nil || n != 2 {
		t.Fatalf("RevokeSessions = %d %v, want 2", n, err)
	}
	for _, tok := range tokens {
		if _, err := f.svc.Authenticate(ctx, tok); !errors.Is(err, usecase.ErrUnauthenticated) {
			t.Fatalf("a revoked session still authenticates: %v", err)
		}
	}
	if len(id.signedOut) != 1 || id.signedOut[0].ID != acc.ID {
		t.Fatalf("hub sign-outs = %v", id.signedOut)
	}
	if n, err := f.svc.RevokeSessions(ctx, acc.ID); err != nil || n != 0 {
		t.Fatalf("again = %d %v", n, err)
	}
	if _, err := f.svc.RevokeSessions(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("an unknown account: %v", err)
	}
}

// TestRenewPass is the household page's « Renouveler » (issue #98): any
// tier and days, from the current end, recorded as money received by hand
// (counted in the revenue) or as a gift at 0 F.
func TestRenewPass(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "r1"}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Subscription(ctx, acc.ID, box)
	sub, err := f.svc.RenewPass(ctx, usecase.RenewInput{Account: acc.ID, BoxID: box, Tier: domain.TierMax, Days: 30, AmountXOF: 2000, Reference: "m1"})
	if err != nil || !sub.ExpiresAt.Equal(before.ExpiresAt.Add(30*24*time.Hour)) {
		t.Fatalf("renew = %+v %v (was %v)", sub, err, before.ExpiresAt)
	}
	pay, err := f.store.PaymentByRef(ctx, usecase.ProviderManual, "m1")
	if err != nil || pay.AmountXOF != 2000 || pay.PassCode != "max_30d" || pay.Status != usecase.PaymentCredited {
		t.Fatalf("manual payment = %+v %v", pay, err)
	}
	// A gift: recorded at 0 F under support, as the « Geste commercial ».
	if _, err := f.svc.RenewPass(ctx, usecase.RenewInput{Account: acc.ID, BoxID: box2, Tier: domain.TierBasic, Days: 10, Reference: "g1"}); err != nil {
		t.Fatal(err)
	}
	if pay, err := f.store.PaymentByRef(ctx, usecase.ProviderSupport, "g1"); err != nil || pay.AmountXOF != 0 || pay.PassCode != "basic_10d" {
		t.Fatalf("gift payment = %+v %v", pay, err)
	}
	if _, err := f.svc.RenewPass(ctx, usecase.RenewInput{Account: acc.ID, BoxID: box, Tier: domain.TierMax, Days: 0, Reference: "x"}); !errors.Is(err, domain.ErrManualPass) {
		t.Fatalf("0 days: %v", err)
	}
	// A manual code is no product a checkout sells.
	if _, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: string(acc.Phone), BoxID: box, PassCode: "max_30d"}); !errors.Is(err, domain.ErrUnknownPass) {
		t.Fatalf("checkout of a manual pass: %v", err)
	}
	// Nor can a NabooPay credit name one.
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_30d", Provider: usecase.ProviderNabooPay, Reference: "n2"}); !errors.Is(err, domain.ErrUnknownPass) {
		t.Fatalf("NabooPay credit of a manual pass: %v", err)
	}
}

// TestLifetimePass: granted, it runs, issues tokens, gets no reminder and
// refuses a renewal; revoked, the pass that ran before comes back.
func TestLifetimePass(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "basic_1m", Provider: usecase.ProviderNabooPay, Reference: "r1"}); err != nil {
		t.Fatal(err)
	}
	paid, _ := f.store.Subscription(ctx, acc.ID, box)
	sub, err := f.svc.GrantLifetime(ctx, acc.ID, box, domain.TierMax)
	if err != nil || !sub.Lifetime || sub.Tier != domain.TierMax {
		t.Fatalf("grant = %+v %v", sub, err)
	}
	if _, err := f.svc.RenewPass(ctx, usecase.RenewInput{Account: acc.ID, BoxID: box, Tier: domain.TierMax, Days: 30, Reference: "m1"}); !errors.Is(err, domain.ErrLifetimePass) {
		t.Fatalf("renew a lifetime pass: %v", err)
	}
	if _, err := f.svc.ExtendPass(ctx, acc.ID, box, 5); !errors.Is(err, domain.ErrLifetimePass) {
		t.Fatalf("extend a lifetime pass: %v", err)
	}

	// Years later, the token still comes, for TokenMaxLifetime as always.
	f.advance(3 * 365 * 24 * time.Hour)
	ent, err := f.svc.IssueToken(ctx, acc.ID, box)
	if err != nil || ent.Tier != domain.TierMax || !ent.Lifetime || !ent.Claims.ExpiresAt.Equal(f.now.Add(domain.TokenMaxLifetime)) {
		t.Fatalf("token = %+v %v", ent, err)
	}
	if _, _, err := domain.Verify(f.pub, ent.Token, f.now); err != nil {
		t.Fatalf("the token does not verify: %v", err)
	}
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 0 {
		t.Fatalf("reminders = %d %v, want none for a lifetime pass", n, err)
	}

	// Revoked: the Basic pass ended meanwhile, so the box is Free.
	back, err := f.svc.RevokeLifetime(ctx, acc.ID, box)
	if err != nil || back.Lifetime || back.Active(f.now) {
		t.Fatalf("revoke = %+v %v (paid until %v)", back, err, paid.ExpiresAt)
	}
	if _, err := f.svc.RevokeLifetime(ctx, acc.ID, box); !errors.Is(err, domain.ErrNotLifetime) {
		t.Fatalf("revoke twice: %v", err)
	}
	if _, err := f.svc.GrantLifetime(ctx, "00000000-0000-0000-0000-000000000000", box, domain.TierMax); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("grant to no account: %v", err)
	}
}
