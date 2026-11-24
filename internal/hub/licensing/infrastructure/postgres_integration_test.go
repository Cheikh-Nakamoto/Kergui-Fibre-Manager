package infrastructure_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agile-factory/kernel/kernel/migrator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/infrastructure"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// testDatabaseEnv names a throwaway Postgres database; `make hub-test` and
// CI set it. The database is shared and never reset, so every test draws
// its own numbers and boxes.
const testDatabaseEnv = "KERGUI_HUB_TEST_DATABASE_URL"

func newRepo(t *testing.T) *infrastructure.Repo {
	t.Helper()
	dsn := os.Getenv(testDatabaseEnv)
	if dsn == "" {
		t.Skipf("%s not set: skipping the Postgres integration test", testDatabaseEnv)
	}
	if err := migrator.Run(dsn, []migrator.Source{
		{Name: "hub_licensing", FS: licensing.Migrations, Path: "migrations"},
	}); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return infrastructure.New(pool)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func newBox() domain.BoxID { return domain.BoxID("ZTEG" + randHex(4)) }

func newPhone() domain.Phone {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return domain.Phone(fmt.Sprintf("+22177%07d", (uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]))%10_000_000))
}

type sender struct {
	mu   sync.Mutex
	last map[alerts.Phone]string
}

func (s *sender) Send(_ context.Context, to alerts.Phone, m alerts.OutboundMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil {
		s.last = map[alerts.Phone]string{}
	}
	s.last[to] = m.Text
	return "", nil
}

// Postgres keeps microseconds; the domain compares instants with Equal.
var t0 = time.Now().UTC().Truncate(time.Second)

func service(t *testing.T, repo *infrastructure.Repo, now *time.Time) (*usecase.Service, *sender) {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(nil)
	s := &sender{}
	return &usecase.Service{Store: repo, Sender: s, SigningKey: priv, Now: func() time.Time { return *now }}, s
}

func TestPostgres_SignInTrialTransferAndToken(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	svc, snd := service(t, repo, &now)
	p := newPhone()

	if _, err := svc.RequestOTP(ctx, string(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestOTP(ctx, string(p)); !errors.Is(err, usecase.ErrOTPRateLimited) {
		t.Fatalf("resend at once: %v", err)
	}
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(snd.last[p])
	if _, err := svc.VerifyOTP(ctx, string(p), "999999x", ""); !errors.Is(err, usecase.ErrOTPInvalid) {
		t.Fatalf("wrong code: %v", err)
	}
	in, err := svc.VerifyOTP(ctx, string(p), code, "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyOTP(ctx, string(p), code, ""); !errors.Is(err, usecase.ErrOTPExpired) {
		t.Fatalf("code reused: %v", err)
	}
	acc, err := svc.Authenticate(ctx, in.Token)
	if err != nil || acc.Phone != p {
		t.Fatalf("authenticate: %+v %v", acc, err)
	}

	b1, b2 := newBox(), newBox()
	// The web trial waits on the number, then starts when the app binds a box.
	if _, err := svc.RequestTrial(ctx, string(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestTrial(ctx, string(p)); !errors.Is(err, domain.ErrTrialPhoneUsed) {
		t.Fatalf("second request: %v", err)
	}
	now = now.Add(2 * 24 * time.Hour)
	bound, err := svc.BindBox(ctx, acc.ID, string(b1))
	sub := bound.Subscription
	if err != nil || bound.Trial != usecase.TrialStarted || sub.Tier != domain.TierMax || !sub.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("trial %+v %v", bound, err)
	}
	if again, err := svc.BindBox(ctx, acc.ID, string(b2)); err != nil || again.Trial != usecase.TrialNone {
		t.Fatalf("second box: %+v %v", again, err)
	}
	// Another number's trial is refused on the box that had one, and the
	// transaction leaves it waiting.
	other, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestTrial(ctx, string(other.Phone)); err != nil {
		t.Fatal(err)
	}
	if out, err := svc.BindBox(ctx, other.ID, string(b1)); err != nil || out.Trial != usecase.TrialBoxUsed || out.Subscription.Tier != domain.TierFree {
		t.Fatalf("trialled box: %+v %v", out, err)
	}
	if _, err := repo.PendingTrial(ctx, other.ID); err != nil {
		t.Fatalf("refused trial no longer waiting: %v", err)
	}
	// The unique constraint holds even when the up-front check is skipped.
	if err := repo.RecordTrial(ctx, usecase.Trial{Phone: newPhone(), BoxID: b1, Account: other.ID, StartedAt: now}); !errors.Is(err, usecase.ErrTrialRace) {
		t.Fatalf("racing trial: %v", err)
	}
	if err := repo.BindTrial(ctx, other.Phone, b1, now); !errors.Is(err, usecase.ErrTrialRace) {
		t.Fatalf("racing bind: %v", err)
	}
	if err := repo.BindTrial(ctx, newPhone(), newBox(), now); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("bind without a pending trial: %v", err)
	}

	moved, err := svc.Transfer(ctx, acc.ID, string(b1), string(b2))
	if err != nil || moved.BoxID != b2 || !moved.ExpiresAt.Equal(sub.ExpiresAt) {
		t.Fatalf("transfer %+v %v", moved, err)
	}
	if _, err := repo.Subscription(ctx, acc.ID, b1); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("old box kept its pass: %v", err)
	}
	if _, err := svc.Transfer(ctx, acc.ID, string(b2), string(b1)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Transfer(ctx, acc.ID, string(b1), string(b2)); !errors.Is(err, domain.ErrTransferLimit) {
		t.Fatalf("third transfer: %v", err)
	}

	ent, err := svc.IssueToken(ctx, acc.ID, string(b1))
	if err != nil {
		t.Fatal(err)
	}
	if c, _, err := domain.Verify(svc.PublicKey(), ent.Token, now); err != nil || c.BoxID != b1 || c.Account != acc.ID {
		t.Fatalf("token: %+v %v", c, err)
	}
	list, err := svc.Subscriptions(ctx, acc.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("subscriptions %v %v", list, err)
	}

	if err := svc.SignOut(ctx, in.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, in.Token); !errors.Is(err, usecase.ErrUnauthenticated) {
		t.Fatalf("after sign-out: %v", err)
	}
}

func TestPostgres_ConcurrentCreditsOfOneReferenceCreditOnce(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	svc, _ := service(t, repo, &now)
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	b := newBox()
	ref := "play-" + randHex(8)

	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, already, err := svc.CreditPass(ctx, usecase.CreditInput{
				Account: acc.ID, BoxID: b, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: ref,
			})
			if err != nil {
				t.Error(err)
				return
			}
			results <- !already
		}()
	}
	wg.Wait()
	close(results)
	credited := 0
	for ok := range results {
		if ok {
			credited++
		}
	}
	if credited != 1 {
		t.Fatalf("credited %d times", credited)
	}
	sub, err := repo.Subscription(ctx, acc.ID, b)
	if err != nil || !sub.ExpiresAt.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("sub %+v %v", sub, err)
	}
	p, err := repo.PaymentByRef(ctx, usecase.ProviderNabooPay, ref)
	if err != nil || p.Status != usecase.PaymentCredited || p.CreditedAt == nil || p.AmountXOF != 2000 {
		t.Fatalf("payment %+v %v", p, err)
	}
}

func TestPostgres_PaymentsAndReminderClaims(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	b := newBox()
	pay := usecase.Payment{
		ID: uuid.NewString(), Provider: usecase.ProviderNabooPay, Reference: "ord-" + randHex(8),
		Account: acc.ID, BoxID: b, PassCode: "basic_1m", AmountXOF: 650, Status: usecase.PaymentPending,
		CheckoutURL: "https://pay.test/x", CreatedAt: now,
	}
	if err := repo.CreatePayment(ctx, pay); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreatePayment(ctx, pay); !errors.Is(err, usecase.ErrPaymentMismatch) {
		t.Fatalf("duplicate reference: %v", err)
	}
	pending, err := repo.PendingPayments(ctx, usecase.ProviderNabooPay, now.Add(-time.Minute), 1000)
	if err != nil || !containsPayment(pending, pay.ID) {
		t.Fatalf("pending: %v", err)
	}
	if err := repo.SetPaymentStatus(ctx, pay.ID, usecase.PaymentCancelled, now); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.PaymentByRef(ctx, pay.Provider, pay.Reference); got.Status != usecase.PaymentCancelled || got.CreditedAt != nil {
		t.Fatalf("cancelled %+v", got)
	}

	sub := domain.Subscription{Account: acc.ID, BoxID: b, Tier: domain.TierBasic, ExpiresAt: now.Add(2 * 24 * time.Hour)}
	if err := repo.SaveSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	due, err := repo.DueReminders(ctx, now, now.Add(domain.ReminderLead), 10_000)
	if err != nil || !containsSub(due, sub) {
		t.Fatalf("due: %v", err)
	}
	ok, err := repo.ClaimReminder(ctx, sub, now)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, _ := repo.ClaimReminder(ctx, sub, now); ok {
		t.Fatal("claimed twice")
	}
	if err := repo.ReleaseReminder(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if ok, _ := repo.ClaimReminder(ctx, sub, now); !ok {
		t.Fatal("not claimable after release")
	}
	got, _ := repo.Subscription(ctx, acc.ID, b)
	if !got.ReminderSentFor.Equal(sub.ExpiresAt) || got.ReminderDue(now) {
		t.Fatalf("after claim %+v", got)
	}
}

// An order pending for longer than the TTL is listed as stale and can be
// deleted while still pending; a fresh one is not listed, and an order
// that moved on is never deleted.
func TestPostgres_StalePendingPaymentsAreDeleted(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(at time.Time) usecase.Payment {
		p := usecase.Payment{
			ID: uuid.NewString(), Provider: usecase.ProviderNabooPay, Reference: "ord-" + randHex(8),
			Account: acc.ID, BoxID: newBox(), PassCode: "basic_1m", AmountXOF: 650, Status: usecase.PaymentPending,
			CheckoutURL: "https://pay.test/x", CreatedAt: at,
		}
		if err := repo.CreatePayment(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, fresh, moved := mk(t0.Add(-10*time.Minute)), mk(t0.Add(-time.Minute)), mk(t0.Add(-10*time.Minute))
	if err := repo.SetPaymentStatus(ctx, moved.ID, usecase.PaymentCancelled, t0); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.StalePendingPayments(ctx, usecase.ProviderNabooPay, t0.Add(-3*time.Minute), 10_000)
	if err != nil || !containsPayment(stale, old.ID) || containsPayment(stale, fresh.ID) || containsPayment(stale, moved.ID) {
		t.Fatalf("stale: %v", err)
	}
	if err := repo.DeletePendingPayment(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PaymentByRef(ctx, old.Provider, old.Reference); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("deleted order still there: %v", err)
	}
	if err := repo.DeletePendingPayment(ctx, old.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if err := repo.DeletePendingPayment(ctx, moved.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("cancelled order deleted: %v", err)
	}
}

// A pass bought without a box is stored with none (NULL), listed as
// waiting once credited, and bound to a box exactly once.
func TestPostgres_WaitingPassesBindOnce(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	newPay := func(code string, at time.Time) usecase.Payment {
		return usecase.Payment{
			ID: uuid.NewString(), Provider: usecase.ProviderNabooPay, Reference: "ord-" + randHex(8),
			Account: acc.ID, PassCode: code, AmountXOF: 2000, Status: usecase.PaymentPending, CreatedAt: at,
		}
	}
	first, second, pending := newPay("max_1m", now), newPay("max_3m", now), newPay("basic_1m", now)
	for _, p := range []usecase.Payment{first, second, pending} {
		if err := repo.CreatePayment(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := repo.PaymentByRef(ctx, first.Provider, first.Reference); err != nil || got.BoxID != "" {
		t.Fatalf("stored %+v %v", got, err)
	}
	// Credited out of order: the list follows the credit time.
	if err := repo.SetPaymentStatus(ctx, second.ID, usecase.PaymentCredited, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPaymentStatus(ctx, first.ID, usecase.PaymentCredited, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	waiting, err := repo.WaitingPasses(ctx, acc.ID)
	if err != nil || len(waiting) != 2 || waiting[0].ID != second.ID || waiting[1].ID != first.ID {
		t.Fatalf("waiting %+v %v", waiting, err)
	}
	b := newBox()
	if err := repo.BindPayment(ctx, second.ID, b); err != nil {
		t.Fatal(err)
	}
	if err := repo.BindPayment(ctx, second.ID, newBox()); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("bound twice: %v", err)
	}
	if got, _ := repo.PaymentByRef(ctx, second.Provider, second.Reference); got.BoxID != b {
		t.Fatalf("bound %+v", got)
	}
	if waiting, _ := repo.WaitingPasses(ctx, acc.ID); len(waiting) != 1 || waiting[0].ID != first.ID {
		t.Fatalf("still waiting %+v", waiting)
	}
}

// The customer area (issue #63): an account's payments newest first, only
// its own, and its trial, pending then bound.
func TestPostgres_AccountPaymentsAndTrial(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	pay := func(a domain.AccountID, at time.Time) usecase.Payment {
		p := usecase.Payment{
			ID: uuid.NewString(), Provider: usecase.ProviderNabooPay, Reference: "ord-" + randHex(8),
			Account: a, PassCode: "max_1m", AmountXOF: 2000, Status: usecase.PaymentPending, CreatedAt: at,
		}
		if err := repo.CreatePayment(ctx, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	older, newer := pay(acc.ID, t0), pay(acc.ID, t0.Add(time.Hour))
	pay(other.ID, t0.Add(2*time.Hour))
	list, err := repo.AccountPayments(ctx, acc.ID, 50)
	if err != nil || len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("payments %+v %v", list, err)
	}
	if list, _ := repo.AccountPayments(ctx, acc.ID, 1); len(list) != 1 {
		t.Fatalf("limit: %d", len(list))
	}

	if _, err := repo.AccountTrial(ctx, acc.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("no trial: %v", err)
	}
	if err := repo.RecordTrial(ctx, usecase.Trial{Phone: acc.Phone, Account: acc.ID, RequestedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if tr, err := repo.AccountTrial(ctx, acc.ID); err != nil || !tr.Pending() || !tr.RequestedAt.Equal(t0) {
		t.Fatalf("pending trial %+v %v", tr, err)
	}
	b := newBox()
	if err := repo.BindTrial(ctx, acc.Phone, b, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if tr, err := repo.AccountTrial(ctx, acc.ID); err != nil || tr.BoxID != b || !tr.StartedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("bound trial %+v %v", tr, err)
	}
}

func containsPayment(list []usecase.Payment, id string) bool {
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}

func containsSub(list []domain.Subscription, s domain.Subscription) bool {
	for _, x := range list {
		if x.Account == s.Account && x.BoxID == s.BoxID {
			return true
		}
	}
	return false
}
