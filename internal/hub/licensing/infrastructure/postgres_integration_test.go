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
	if _, err := svc.VerifyOTP(ctx, string(p), code, ""); !errors.Is(err, usecase.ErrOTPInvalid) {
		t.Fatalf("code reused: %v", err)
	}
	acc, err := svc.Authenticate(ctx, in.Token)
	if err != nil || acc.Phone != p {
		t.Fatalf("authenticate: %+v %v", acc, err)
	}

	b1, b2 := newBox(), newBox()
	sub, err := svc.StartTrial(ctx, acc.ID, string(b1))
	if err != nil || sub.Tier != domain.TierMax || !sub.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("trial %+v %v", sub, err)
	}
	if _, err := svc.StartTrial(ctx, acc.ID, string(b2)); !errors.Is(err, domain.ErrTrialPhoneUsed) {
		t.Fatalf("second trial: %v", err)
	}
	// Another account racing for the same box hits the unique constraint
	// even when its up-front check is skipped.
	other, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordTrial(ctx, usecase.Trial{Phone: other.Phone, BoxID: b1, Account: other.ID, StartedAt: now}); !errors.Is(err, usecase.ErrTrialRace) {
		t.Fatalf("racing trial: %v", err)
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
				Account: acc.ID, BoxID: b, PassCode: "max_1m", Provider: usecase.ProviderPlay, Reference: ref,
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
	p, err := repo.PaymentByRef(ctx, usecase.ProviderPlay, ref)
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
