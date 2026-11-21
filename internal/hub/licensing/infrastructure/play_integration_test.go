package infrastructure_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

func TestPostgres_PlayBindingsAndChannel(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	b, b2 := newBox(), newBox()
	tok1, tok2 := "tok-"+randHex(16), "tok-"+randHex(16)

	if _, err := repo.PlayBinding(ctx, tok1); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("unbound: %v", err)
	}
	if _, err := repo.LatestPlayBinding(ctx, acc.ID, b); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("no latest: %v", err)
	}
	first := usecase.PlayBinding{Token: tok1, Account: acc.ID, BoxID: b, ProductID: "kergui_basic", BoundAt: now}
	got, err := repo.BindPlayPurchase(ctx, first)
	if err != nil || got != first {
		t.Fatalf("bind %+v %v", got, err)
	}
	// Binding a bound token again returns the first binding, unchanged.
	got, err = repo.BindPlayPurchase(ctx, usecase.PlayBinding{Token: tok1, Account: other.ID, BoxID: b2, ProductID: "kergui_max", BoundAt: now})
	if err != nil || got != first {
		t.Fatalf("rebind %+v %v", got, err)
	}
	if _, err := repo.BindPlayPurchase(ctx, usecase.PlayBinding{Token: tok2, Account: acc.ID, BoxID: b, ProductID: "kergui_max", BoundAt: now}); err != nil {
		t.Fatal(err)
	}
	if l, err := repo.LatestPlayBinding(ctx, acc.ID, b); err != nil || l.Token != tok2 {
		t.Fatalf("latest %+v %v", l, err)
	}
	if err := repo.RebindPlayPurchases(ctx, acc.ID, b, b2); err != nil {
		t.Fatal(err)
	}
	if l, err := repo.LatestPlayBinding(ctx, acc.ID, b2); err != nil || l.Token != tok2 {
		t.Fatalf("after rebind %+v %v", l, err)
	}
	if g, _ := repo.PlayBinding(ctx, tok1); g.BoxID != b2 {
		t.Fatalf("tok1 not moved: %+v", g)
	}

	// The channel round-trips, and Play subscriptions get no reminder.
	play := domain.Subscription{Account: acc.ID, BoxID: b2, Tier: domain.TierMax, ExpiresAt: now.Add(24 * time.Hour), Channel: domain.ChannelPlay}
	prepaid := domain.Subscription{Account: acc.ID, BoxID: b, Tier: domain.TierBasic, ExpiresAt: now.Add(24 * time.Hour)}
	for _, s := range []domain.Subscription{play, prepaid} {
		if err := repo.SaveSubscription(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if s, _ := repo.Subscription(ctx, acc.ID, b2); s.Channel != domain.ChannelPlay {
		t.Fatalf("play channel lost: %+v", s)
	}
	if s, _ := repo.Subscription(ctx, acc.ID, b); s.Channel != domain.ChannelPrepaid {
		t.Fatalf("prepaid channel: %+v", s)
	}
	due, err := repo.DueReminders(ctx, now, now.Add(domain.ReminderLead), 10_000)
	if err != nil || containsSub(due, play) || !containsSub(due, prepaid) {
		t.Fatalf("due: %v", err)
	}
}

// fakePlay answers one purchase.
type fakePlay struct {
	mu sync.Mutex
	p  usecase.PlayPurchase
}

func (f *fakePlay) Purchase(context.Context, string) (usecase.PlayPurchase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p, nil
}

func (f *fakePlay) Acknowledge(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.p.Acknowledged = true
	return nil
}

func TestPostgres_PlayPurchaseRenewalAndConcurrentClaims(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	svc, _ := service(t, repo, &now)
	exp := now.Add(30 * 24 * time.Hour)
	g := &fakePlay{p: usecase.PlayPurchase{State: usecase.PlayStateActive,
		LineItems: []usecase.PlayLineItem{{ProductID: "kergui_max", ExpiresAt: exp}}}}
	svc.Play = g

	var accs []domain.AccountID
	for range 4 {
		a, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		accs = append(accs, a.ID)
	}
	b, tok := newBox(), "tok-"+randHex(16)

	// Four accounts report the same token at once: one gets it.
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		won     []domain.AccountID
		refused int
	)
	for _, a := range accs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.PlayPurchase(ctx, usecase.PlayPurchaseInput{Account: a, BoxID: string(b), ProductID: "kergui_max", PurchaseToken: tok})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won = append(won, a)
			case errors.Is(err, usecase.ErrPlayTokenBound):
				refused++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(won) != 1 || refused != 3 {
		t.Fatalf("won %v, refused %d", won, refused)
	}
	owner := won[0]
	sub, err := repo.Subscription(ctx, owner, b)
	if err != nil || sub.Channel != domain.ChannelPlay || !sub.ExpiresAt.Equal(exp) || sub.Tier != domain.TierMax {
		t.Fatalf("sub %+v %v", sub, err)
	}

	// A renewal, told twice, credits once.
	exp2 := exp.Add(30 * 24 * time.Hour)
	g.mu.Lock()
	g.p.LineItems[0].ExpiresAt = exp2
	g.mu.Unlock()
	for i, want := range []usecase.PlayOutcome{usecase.PlayCredited, usecase.PlayUnchanged} {
		res, err := svc.PlayNotify(ctx, usecase.PlayNotification{PurchaseToken: tok, Type: usecase.PlayRenewed})
		if err != nil || res.Outcome != want || !res.Subscription.ExpiresAt.Equal(exp2) {
			t.Fatalf("renewal %d: %+v %v", i, res, err)
		}
	}
	if p, err := repo.PaymentByRef(ctx, usecase.ProviderPlay, usecase.PlayReference(tok, exp2)); err != nil || p.Status != usecase.PaymentCredited {
		t.Fatalf("renewal payment %+v %v", p, err)
	}

	// Revoked: Google says expired, the box ends now.
	g.mu.Lock()
	g.p.State = usecase.PlayStateExpired
	g.mu.Unlock()
	res, err := svc.PlayNotify(ctx, usecase.PlayNotification{PurchaseToken: tok, Type: usecase.PlayRevoked})
	if err != nil || res.Outcome != usecase.PlayEnded {
		t.Fatalf("revoke %+v %v", res, err)
	}
	if s, _ := repo.Subscription(ctx, owner, b); s.Active(now) || s.Channel != domain.ChannelPlay {
		t.Fatalf("after revoke %+v", s)
	}
}
