package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

const playPkg = "sn.kergui.app"

// fakePlay is Google: a purchase per token, as the test sets it.
type fakePlay struct {
	mu        sync.Mutex
	purchases map[string]usecase.PlayPurchase
	acks      []string
	ackErr    error
	err       error
	calls     int
}

func (g *fakePlay) Purchase(_ context.Context, token string) (usecase.PlayPurchase, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if g.err != nil {
		return usecase.PlayPurchase{}, g.err
	}
	p, ok := g.purchases[token]
	if !ok {
		return usecase.PlayPurchase{}, usecase.ErrPlayPurchaseInvalid
	}
	return p, nil
}

func (g *fakePlay) Acknowledge(_ context.Context, productID, token string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ackErr != nil {
		return g.ackErr
	}
	g.acks = append(g.acks, productID+"/"+token)
	if p, ok := g.purchases[token]; ok {
		p.Acknowledged = true
		g.purchases[token] = p
	}
	return nil
}

func (g *fakePlay) set(token string, p usecase.PlayPurchase) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.purchases[token] = p
}

func playSub(state usecase.PlayState, product string, exp time.Time) usecase.PlayPurchase {
	return usecase.PlayPurchase{State: state, LineItems: []usecase.PlayLineItem{{ProductID: product, ExpiresAt: exp}}}
}

func withPlay(f *fixture) *fakePlay {
	g := &fakePlay{purchases: map[string]usecase.PlayPurchase{}}
	f.svc.Play, f.svc.PlayPackage = g, playPkg
	return g
}

func (f *fixture) buy(acc usecase.Account, boxID, product, token string) (domain.Subscription, error) {
	return f.svc.PlayPurchase(context.Background(), usecase.PlayPurchaseInput{
		Account: acc.ID, BoxID: boxID, ProductID: product, PurchaseToken: token,
	})
}

func TestPlayPurchase_VerifiesAcknowledgesBindsAndSetsPlaysExpiry(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	acc := f.signIn(t)
	exp := t0.Add(30*24*time.Hour + 3*time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_max", exp))

	sub, err := f.buy(acc, box, "kergui_max", "tok-1")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Tier != domain.TierMax || !sub.ExpiresAt.Equal(exp) || sub.Channel != domain.ChannelPlay {
		t.Fatalf("sub %+v", sub)
	}
	if len(g.acks) != 1 || g.acks[0] != "kergui_max/tok-1" {
		t.Fatalf("acks %v", g.acks)
	}
	// The app sends the token again on every launch: nothing moves, and an
	// acknowledged purchase is not acknowledged twice.
	again, err := f.buy(acc, box, "kergui_max", "tok-1")
	if err != nil || again != sub || len(g.acks) != 1 {
		t.Fatalf("replay: %+v %v acks=%v", again, err, g.acks)
	}
	pay, err := f.store.PaymentByRef(context.Background(), usecase.ProviderPlay, usecase.PlayReference("tok-1", exp))
	if err != nil || pay.Status != usecase.PaymentCredited || pay.PassCode != "kergui_max" || pay.AmountXOF != 2000 {
		t.Fatalf("payment %+v %v", pay, err)
	}
	// The entitlement token follows Play's expiry.
	ent, err := f.svc.IssueToken(context.Background(), acc.ID, box)
	if err != nil || ent.Tier != domain.TierMax {
		t.Fatalf("token %+v %v", ent, err)
	}
	// A Play subscription renews by itself: no WhatsApp reminder.
	f.now = exp.Add(-24 * time.Hour)
	if n, err := f.svc.SendReminders(context.Background()); err != nil || n != 0 {
		t.Fatalf("reminders %d %v", n, err)
	}
}

func TestPlayPurchase_Refusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, err := f.buy(acc, box, "kergui_max", "tok"); !errors.Is(err, usecase.ErrPlayOff) {
		t.Fatalf("play off: %v", err)
	}
	g := withPlay(f)
	exp := t0.Add(30 * 24 * time.Hour)
	g.set("tok-max", playSub(usecase.PlayStateActive, "kergui_max", exp))
	g.set("tok-pending", playSub(usecase.PlayStatePending, "kergui_max", exp))
	g.set("tok-expired", playSub(usecase.PlayStateExpired, "kergui_max", t0.Add(-time.Hour)))
	g.set("tok-hold", playSub(usecase.PlayStateOnHold, "kergui_max", t0.Add(time.Hour)))
	g.set("tok-lapsed", playSub(usecase.PlayStateActive, "kergui_max", t0.Add(-time.Minute)))
	other := playSub(usecase.PlayStateActive, "kergui_basic", exp)
	other.ObfuscatedAccountID = "someone-else"
	g.set("tok-foreign", other)

	for name, tc := range map[string]struct {
		box, product, token string
		want                error
	}{
		"bad box":         {"bad box", "kergui_max", "tok-max", domain.ErrInvalidBoxID},
		"unknown product": {box, "kergui_pro", "tok-max", domain.ErrUnknownProduct},
		"empty token":     {box, "kergui_max", " ", usecase.ErrPlayPurchaseInvalid},
		"unknown token":   {box, "kergui_max", "tok-nope", usecase.ErrPlayPurchaseInvalid},
		"other product":   {box, "kergui_basic", "tok-max", usecase.ErrPlayPurchaseInvalid},
		"pending":         {box, "kergui_max", "tok-pending", usecase.ErrPlayPurchasePending},
		"expired":         {box, "kergui_max", "tok-expired", usecase.ErrPlayPurchaseInactive},
		"on hold":         {box, "kergui_max", "tok-hold", usecase.ErrPlayPurchaseInactive},
		"past its expiry": {box, "kergui_max", "tok-lapsed", usecase.ErrPlayPurchaseInactive},
		"another account": {box, "kergui_basic", "tok-foreign", usecase.ErrPlayTokenBound},
	} {
		if _, err := f.buy(acc, tc.box, tc.product, tc.token); !errors.Is(err, tc.want) {
			t.Errorf("%s: want %v, got %v", name, tc.want, err)
		}
	}
	if _, err := f.store.PlayBinding(ctx, "tok-pending"); !errors.Is(err, usecase.ErrNotFound) {
		t.Error("a refused purchase was bound")
	}

	g.err = usecase.ErrPlayUnavailable
	if _, err := f.buy(acc, box, "kergui_max", "tok-max"); !errors.Is(err, usecase.ErrPlayUnavailable) {
		t.Errorf("Google down: %v", err)
	}
}

func TestPlayPurchase_ATokenBelongsToOneAccountAndBox(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	ctx := context.Background()
	acc := f.signIn(t)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_basic", t0.Add(30*24*time.Hour)))
	if _, err := f.buy(acc, box, "kergui_basic", "tok-1"); err != nil {
		t.Fatal(err)
	}
	// Another box of the same account: that is a transfer, not a purchase.
	if _, err := f.buy(acc, box2, "kergui_basic", "tok-1"); !errors.Is(err, usecase.ErrPlayTokenBound) {
		t.Fatalf("other box: %v", err)
	}
	// Another account replaying a token it saw: refused.
	if _, err := f.svc.RequestOTP(ctx, "+221770000002"); err != nil {
		t.Fatal(err)
	}
	in, err := f.svc.VerifyOTP(ctx, "+221770000002", f.sender.code(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.buy(in.Account, box, "kergui_basic", "tok-1"); !errors.Is(err, usecase.ErrPlayTokenBound) {
		t.Fatalf("other account: %v", err)
	}
	if _, err := f.svc.IssueToken(ctx, in.Account.ID, box); err != nil {
		t.Fatal(err)
	}
	if subs, _ := f.svc.Subscriptions(ctx, in.Account.ID); len(subs) != 0 {
		t.Fatalf("the thief got %+v", subs)
	}
}

func TestPlayPurchase_AckFailureIsRetriedOnTheNextCall(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	acc := f.signIn(t)
	exp := t0.Add(30 * 24 * time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_max", exp))
	g.ackErr = usecase.ErrPlayUnavailable
	if _, err := f.buy(acc, box, "kergui_max", "tok-1"); !errors.Is(err, usecase.ErrPlayUnavailable) {
		t.Fatalf("ack failure must surface so the app retries: %v", err)
	}
	g.ackErr = nil
	sub, err := f.buy(acc, box, "kergui_max", "tok-1")
	if err != nil || !sub.ExpiresAt.Equal(exp) || len(g.acks) != 1 {
		t.Fatalf("retry: %+v %v %v", sub, err, g.acks)
	}
}

func TestPlayPurchase_FreeTrialIsRecordedForThePhoneAndBox(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	ctx := context.Background()
	acc := f.signIn(t)
	p := playSub(usecase.PlayStateActive, "kergui_max", t0.Add(7*24*time.Hour))
	p.LineItems[0].FreeTrial = true
	g.set("tok-trial", p)
	if _, err := f.buy(acc, box, "kergui_max", "tok-trial"); err != nil {
		t.Fatal(err)
	}
	phoneUsed, boxUsed, err := f.store.TrialUsed(ctx, phone, box)
	if err != nil || !phoneUsed || !boxUsed {
		t.Fatalf("trial not recorded: %v %v %v", phoneUsed, boxUsed, err)
	}
	// The hub's own trial is then refused on that box.
	if _, err := f.svc.StartTrial(ctx, acc.ID, box2); !errors.Is(err, domain.ErrTrialPhoneUsed) {
		t.Fatalf("hub trial after Play trial: %v", err)
	}
}

// --- RTDN ------------------------------------------------------------------------

func notify(f *fixture, token string) (usecase.PlaySync, error) {
	return f.svc.PlayNotify(context.Background(), usecase.PlayNotification{
		PackageName: playPkg, PurchaseToken: token, Type: usecase.PlayRenewed,
	})
}

func TestPlayNotify_FollowsGooglesStateNotTheNotification(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	ctx := context.Background()
	acc := f.signIn(t)
	exp1 := t0.Add(30 * 24 * time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_basic", exp1))
	if _, err := f.buy(acc, box, "kergui_basic", "tok-1"); err != nil {
		t.Fatal(err)
	}

	// Renewed: Google's new expiry, credited once however often it is told.
	exp2 := exp1.Add(30 * 24 * time.Hour)
	f.now = exp1.Add(-time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_basic", exp2))
	res, err := notify(f, "tok-1")
	if err != nil || res.Outcome != usecase.PlayCredited || !res.Subscription.ExpiresAt.Equal(exp2) {
		t.Fatalf("renewal: %+v %v", res, err)
	}
	if res, _ = notify(f, "tok-1"); res.Outcome != usecase.PlayUnchanged {
		t.Fatalf("replayed renewal: %+v", res)
	}
	if _, err := f.store.PaymentByRef(ctx, usecase.ProviderPlay, usecase.PlayReference("tok-1", exp2)); err != nil {
		t.Fatalf("renewal payment: %v", err)
	}

	// Canceled (auto-renew off): it runs until its expiry.
	g.set("tok-1", playSub(usecase.PlayStateCanceled, "kergui_basic", exp2))
	if res, _ = notify(f, "tok-1"); res.Outcome != usecase.PlayUnchanged || !res.Subscription.Active(f.now) {
		t.Fatalf("canceled: %+v", res)
	}

	// Revoked (refund): Google says expired, the box goes Free now.
	g.set("tok-1", playSub(usecase.PlayStateExpired, "kergui_basic", f.now))
	res, err = notify(f, "tok-1")
	if err != nil || res.Outcome != usecase.PlayEnded || res.Subscription.Active(f.now) || !res.Subscription.ExpiresAt.Equal(f.now) {
		t.Fatalf("revoked: %+v %v", res, err)
	}
	if ent, _ := f.svc.IssueToken(ctx, acc.ID, box); ent.Tier != domain.TierFree {
		t.Fatalf("token after revoke: %+v", ent)
	}

	// Recovered from hold: a new expiry, credited again.
	exp3 := f.now.Add(30 * 24 * time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_basic", exp3))
	if res, _ = notify(f, "tok-1"); res.Outcome != usecase.PlayCredited || !res.Subscription.ExpiresAt.Equal(exp3) {
		t.Fatalf("recovered: %+v", res)
	}

	// A notification that says "renewed" while Google says on hold ends it:
	// the body is a hint, Google's answer is the state.
	g.set("tok-1", playSub(usecase.PlayStateOnHold, "kergui_basic", exp3))
	if res, _ = notify(f, "tok-1"); res.Outcome != usecase.PlayEnded {
		t.Fatalf("on hold: %+v", res)
	}
}

func TestPlayNotify_Ignores(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	ctx := context.Background()
	exp := t0.Add(30 * 24 * time.Hour)
	g.set("tok-unbound", playSub(usecase.PlayStateActive, "kergui_max", exp))

	for name, n := range map[string]usecase.PlayNotification{
		"test":          {PackageName: playPkg, Test: true},
		"other package": {PackageName: "com.other", PurchaseToken: "tok-unbound"},
		"no token":      {PackageName: playPkg},
		"unbound":       {PackageName: playPkg, PurchaseToken: "tok-unbound"},
		"unknown":       {PackageName: playPkg, PurchaseToken: "tok-nope"},
	} {
		res, err := f.svc.PlayNotify(ctx, n)
		if err != nil || res.Outcome != usecase.PlayIgnored {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	if len(g.acks) != 0 {
		t.Fatalf("an unbound purchase was acknowledged: %v", g.acks)
	}
	g.err = usecase.ErrPlayUnavailable
	if _, err := f.svc.PlayNotify(ctx, usecase.PlayNotification{PackageName: playPkg, PurchaseToken: "tok-unbound"}); !errors.Is(err, usecase.ErrPlayUnavailable) {
		t.Fatalf("Google down must be retried: %v", err)
	}
}

func TestPlayNotify_UpgradeFollowsTheLinkedTokenAndTheOldOneStopsCounting(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	acc := f.signIn(t)
	exp := t0.Add(30 * 24 * time.Hour)
	g.set("tok-basic", playSub(usecase.PlayStateActive, "kergui_basic", exp))
	if _, err := f.buy(acc, box, "kergui_basic", "tok-basic"); err != nil {
		t.Fatal(err)
	}
	// The upgrade: a new token linked to the old one, which Google expires.
	// The new one's notification arrives before the app calls the hub.
	f.advance(5 * 24 * time.Hour)
	maxExp := f.now.Add(30 * 24 * time.Hour)
	up := playSub(usecase.PlayStateActive, "kergui_max", maxExp)
	up.LinkedPurchaseToken = "tok-basic"
	g.set("tok-max", up)
	g.set("tok-basic", playSub(usecase.PlayStateExpired, "kergui_basic", f.now))

	res, err := notify(f, "tok-max")
	if err != nil || res.Outcome != usecase.PlayCredited || res.Subscription.Tier != domain.TierMax || !res.Subscription.ExpiresAt.Equal(maxExp) {
		t.Fatalf("upgrade: %+v %v", res, err)
	}
	b, err := f.store.PlayBinding(context.Background(), "tok-max")
	if err != nil || b.Account != acc.ID || b.BoxID != box {
		t.Fatalf("binding %+v %v", b, err)
	}
	// The old token's "expired" must not end the new Max subscription.
	if res, _ = notify(f, "tok-basic"); res.Outcome != usecase.PlayIgnored || !res.Subscription.Active(f.now) {
		t.Fatalf("superseded token: %+v", res)
	}
	// And the app's late call with the new token changes nothing.
	if sub, err := f.buy(acc, box, "kergui_max", "tok-max"); err != nil || !sub.ExpiresAt.Equal(maxExp) {
		t.Fatalf("late app call: %+v %v", sub, err)
	}
}

func TestTransfer_MovesThePlayBindingWithThePass(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	ctx := context.Background()
	acc := f.signIn(t)
	exp := t0.Add(30 * 24 * time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_max", exp))
	if _, err := f.buy(acc, box, "kergui_max", "tok-1"); err != nil {
		t.Fatal(err)
	}
	moved, err := f.svc.Transfer(ctx, acc.ID, box, box2)
	if err != nil || moved.Channel != domain.ChannelPlay {
		t.Fatalf("transfer: %+v %v", moved, err)
	}
	// The renewal lands on the new box.
	exp2 := exp.Add(30 * 24 * time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_max", exp2))
	res, err := notify(f, "tok-1")
	if err != nil || res.Subscription.BoxID != box2 || !res.Subscription.ExpiresAt.Equal(exp2) {
		t.Fatalf("renewal after transfer: %+v %v", res, err)
	}
	// A replay of the purchase already credited before the move is still a
	// replay, not a mismatch.
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_max", exp))
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{
		Account: acc.ID, BoxID: box2, PassCode: "kergui_max", Provider: usecase.ProviderPlay,
		Reference: usecase.PlayReference("tok-1", exp), ExpiresAt: exp,
	}); err != nil {
		t.Fatalf("replay after transfer: %v", err)
	}
	if subs, _ := f.svc.Subscriptions(ctx, acc.ID); len(subs) != 1 || subs[0].BoxID != box2 {
		t.Fatalf("subs %+v", subs)
	}
}

func TestPlayPurchase_AnInactiveBoundPurchaseEndsTheBoxWithoutRTDN(t *testing.T) {
	f := newFixture(t)
	g := withPlay(f)
	acc := f.signIn(t)
	g.set("tok-1", playSub(usecase.PlayStateActive, "kergui_max", t0.Add(30*24*time.Hour)))
	if _, err := f.buy(acc, box, "kergui_max", "tok-1"); err != nil {
		t.Fatal(err)
	}
	f.advance(24 * time.Hour)
	g.set("tok-1", playSub(usecase.PlayStateExpired, "kergui_max", f.now)) // refunded and revoked
	if _, err := f.buy(acc, box, "kergui_max", "tok-1"); !errors.Is(err, usecase.ErrPlayPurchaseInactive) {
		t.Fatalf("revoked: %v", err)
	}
	if ent, _ := f.svc.IssueToken(context.Background(), acc.ID, box); ent.Tier != domain.TierFree {
		t.Fatalf("still entitled after revoke: %+v", ent)
	}
}
