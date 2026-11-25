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
	"github.com/jackc/pgx/v5"
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
}

// reminderWindow is the window a sweep at now looks at.
func reminderWindow(now time.Time) usecase.ReminderQuery {
	day := domain.DayStart(now)
	return usecase.ReminderQuery{DayStart: day, DayEnd: day.Add(24 * time.Hour), Until: now.Add(domain.ReminderLead), Tries: domain.ReminderTries}
}

// reminderRow is the log row of a reminder.
type reminderRow struct {
	whatsapp, push, note string
	tries, phones, sent  int
}

func readReminder(t *testing.T, d usecase.DueReminder) (reminderRow, bool) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var r reminderRow
	err = pool.QueryRow(context.Background(), `
		SELECT whatsapp, push, note, whatsapp_tries, push_phones, push_sent FROM hub_licensing_reminders
		WHERE account_id = $1 AND box_id = $2 AND expires_at = $3 AND step = $4`,
		string(d.Sub.Account), string(d.Sub.BoxID), d.Sub.ExpiresAt, string(d.Step)).
		Scan(&r.whatsapp, &r.push, &r.note, &r.tries, &r.phones, &r.sent)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

// TestPostgres_ReminderStepsAndClaims is issue #99 on Postgres: the J-3 and
// J0 steps from the end date, one claim per end date and step (two sweeps,
// two hub instances or a restart claim it once), the per-channel results,
// the WhatsApp retries, and the passes left out (lifetime, Play, free,
// renewed).
func TestPostgres_ReminderStepsAndClaims(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	// A fixed morning: the store does not look at the hours, but the step
	// depends on the day.
	now := time.Date(2031, 3, 10, 10, 0, 0, 0, time.UTC)
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	save := func(s domain.Subscription) domain.Subscription {
		t.Helper()
		if err := repo.SaveSubscription(ctx, s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	j3 := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierBasic, ExpiresAt: now.Add(2 * 24 * time.Hour)})
	today := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierMax, ExpiresAt: now.Add(5 * time.Hour)})
	endedToday := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierMax, ExpiresAt: now.Add(-8 * time.Hour)})
	later := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierMax, ExpiresAt: now.Add(4 * 24 * time.Hour)})
	yesterday := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierMax, ExpiresAt: now.Add(-11 * time.Hour)})
	play := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierMax, ExpiresAt: now.Add(24 * time.Hour), Channel: domain.ChannelPlay})
	free := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierFree, ExpiresAt: now.Add(24 * time.Hour)})
	life, err := domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierBasic, ExpiresAt: now.Add(24 * time.Hour)}.GrantLifetime(domain.TierMax)
	if err != nil {
		t.Fatal(err)
	}
	save(life)
	// A lifetime pass whose kept-aside pass would be due is not reminded
	// either: its far end is outside any window, and the flag says so.
	lifeNear := save(domain.Subscription{Account: acc.ID, BoxID: newBox(), Tier: domain.TierMax, ExpiresAt: now.Add(24 * time.Hour), Lifetime: true})

	due := func() map[domain.BoxID]usecase.DueReminder {
		t.Helper()
		list, err := repo.DueReminders(ctx, reminderWindow(now), 10_000)
		if err != nil {
			t.Fatal(err)
		}
		out := map[domain.BoxID]usecase.DueReminder{}
		for _, d := range list {
			if d.Sub.Account == acc.ID {
				out[d.Sub.BoxID] = d
			}
		}
		return out
	}
	got := due()
	for _, tc := range []struct {
		sub  domain.Subscription
		step domain.ReminderStep
	}{{j3, domain.ReminderBefore}, {today, domain.ReminderDay}, {endedToday, domain.ReminderDay}} {
		d, ok := got[tc.sub.BoxID]
		if !ok || d.Step != tc.step || d.Retry || !d.Sub.ExpiresAt.Equal(tc.sub.ExpiresAt) {
			t.Errorf("%s: due %+v %v, want step %s", tc.sub.BoxID, d, ok, tc.step)
		}
	}
	for _, s := range []domain.Subscription{later, yesterday, play, free, life, lifeNear} {
		if _, ok := got[s.BoxID]; ok {
			t.Errorf("%+v is due", s)
		}
	}
	if len(got) != 3 {
		t.Fatalf("due = %+v", got)
	}

	// One claim per end date and step, whoever asks.
	d := got[j3.BoxID]
	if ok, err := repo.ClaimReminder(ctx, d, now); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if ok, err := repo.ClaimReminder(ctx, d, now); err != nil || ok {
		t.Fatalf("claimed twice: %v %v", ok, err)
	}
	if r, ok := readReminder(t, d); !ok || r.whatsapp != "sending" || r.push != "sending" || r.tries != 1 {
		t.Fatalf("claimed row %+v %v", r, ok)
	}
	if _, ok := due()[j3.BoxID]; ok {
		t.Fatal("a claimed reminder is still due")
	}
	// The other step of the same end date is its own.
	if ok, err := repo.ClaimReminder(ctx, usecase.DueReminder{Sub: j3, Step: domain.ReminderDay}, now); err != nil || !ok {
		t.Fatalf("J0 of the same end: %v %v", ok, err)
	}

	// WhatsApp failed, the app reached 1 phone of 2: WhatsApp is due again,
	// as a retry, until it failed ReminderTries times.
	if err := repo.RecordReminder(ctx, d, usecase.ReminderOutcome{WhatsApp: usecase.ReminderFailed, Push: usecase.ReminderSent, PushPhones: 2, PushSent: 1}); err != nil {
		t.Fatal(err)
	}
	for try := 2; try <= domain.ReminderTries; try++ {
		r, ok := due()[j3.BoxID]
		if !ok || !r.Retry || r.Step != domain.ReminderBefore {
			t.Fatalf("try %d: due %+v %v", try, r, ok)
		}
		if ok, err := repo.ClaimReminder(ctx, r, now.Add(time.Duration(try)*10*time.Minute)); err != nil || !ok {
			t.Fatalf("try %d: claim %v %v", try, ok, err)
		}
		if ok, _ := repo.ClaimReminder(ctx, r, now); ok {
			t.Fatalf("try %d claimed twice", try)
		}
		// The retry writes WhatsApp alone: the app's result stays.
		if err := repo.RecordReminder(ctx, r, usecase.ReminderOutcome{WhatsApp: usecase.ReminderFailed}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := due()[j3.BoxID]; ok {
		t.Fatal("due after the last try")
	}
	if r, _ := readReminder(t, d); r.whatsapp != "failed" || r.tries != domain.ReminderTries || r.push != "sent" || r.phones != 2 || r.sent != 1 {
		t.Fatalf("after the tries %+v", r)
	}

	// A skipped reminder keeps why.
	sk := got[today.BoxID]
	if ok, err := repo.ClaimReminder(ctx, sk, now); err != nil || !ok {
		t.Fatal(err)
	}
	if err := repo.RecordReminder(ctx, sk, usecase.ReminderOutcome{WhatsApp: usecase.ReminderSkipped, Push: usecase.ReminderSkipped, Note: usecase.NoteTrialConverted}); err != nil {
		t.Fatal(err)
	}
	if r, _ := readReminder(t, sk); r.whatsapp != "skipped" || r.note != usecase.NoteTrialConverted {
		t.Fatalf("skipped %+v", r)
	}

	// Renewed meanwhile: the claim of the old end date fails, and the new
	// end date is not due yet.
	e := got[endedToday.BoxID]
	save(domain.Subscription{Account: acc.ID, BoxID: endedToday.BoxID, Tier: domain.TierMax, ExpiresAt: now.Add(30 * 24 * time.Hour)})
	if ok, err := repo.ClaimReminder(ctx, e, now); err != nil || ok {
		t.Fatalf("claimed the old end of a renewed pass: %v %v", ok, err)
	}
	if _, ok := due()[endedToday.BoxID]; ok {
		t.Fatal("a renewed pass is due")
	}
	if err := repo.RecordReminder(ctx, e, usecase.ReminderOutcome{WhatsApp: usecase.ReminderSent}); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("record without a claim: %v", err)
	}
}

// Deleting an account forgets the number and the box, but not that they
// had the trial: a keyed digest of each stays, so a re-created account is
// refused a second trial. A hub with another key does not recognise them.
func TestPostgres_DeletedAccountKeepsItsTrialMark(t *testing.T) {
	repo := newRepo(t)
	repo.SetTrialMarkKey([]byte("test-trial-mark-key-0123456789abcdef"))
	ctx := context.Background()
	p, b := newPhone(), newBox()
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: p, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordTrial(ctx, usecase.Trial{Phone: p, BoxID: b, Account: acc.ID, RequestedAt: t0, StartedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AccountByID(ctx, acc.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("account still there: %v", err)
	}
	phoneUsed, boxUsed, err := repo.TrialUsed(ctx, p, b)
	if err != nil || !phoneUsed || !boxUsed {
		t.Fatalf("after deletion: phone %v box %v %v, want both used", phoneUsed, boxUsed, err)
	}
	if phoneUsed, _, _ := repo.TrialUsed(ctx, newPhone(), ""); phoneUsed {
		t.Fatal("another number counted as used")
	}
	// Neither the number nor the box is kept in clear.
	pool, err := pgxpool.New(ctx, os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hub_licensing_trial_marks
		WHERE position($1::bytea in digest) > 0 OR position($2::bytea in digest) > 0`, []byte(p), []byte(b)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("clear values in the marks: %d %v", n, err)
	}
	other := newRepo(t)
	other.SetTrialMarkKey([]byte("another-key-another-key-another-key"))
	if phoneUsed, boxUsed, _ := other.TrialUsed(ctx, p, b); phoneUsed || boxUsed {
		t.Fatal("the marks are recognised without the key")
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

func containsSub(list []usecase.DueReminder, s domain.Subscription) bool {
	for _, x := range list {
		if x.Sub.Account == s.Account && x.Sub.BoxID == s.BoxID {
			return true
		}
	}
	return false
}

// TestPostgres_DeleteAccount is the licensing-only deletion (#37): the
// account and its rows go, another account's stay.
func TestPostgres_DeleteAccount(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	now := t0
	svc, snd := service(t, repo, &now)
	p := newPhone()
	if _, err := svc.RequestOTP(ctx, string(p)); err != nil {
		t.Fatal(err)
	}
	in, err := svc.VerifyOTP(ctx, string(p), regexp.MustCompile(`\b\d{6}\b`).FindString(snd.last[p]), "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestTrial(ctx, string(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BindBox(ctx, in.Account.ID, string(newBox())); err != nil {
		t.Fatal(err)
	}
	other, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteAccount(ctx, in.Account); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, in.Token); !errors.Is(err, usecase.ErrUnauthenticated) {
		t.Fatalf("session after the deletion: %v", err)
	}
	if _, err := repo.AccountByID(ctx, in.Account.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("account after the deletion: %v", err)
	}
	// The trial row goes with the account; only its keyed mark stays (see
	// TestPostgres_DeletedAccountKeepsItsTrialMark).
	if _, err := repo.AccountTrial(ctx, in.Account.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("the trial is still recorded: %v", err)
	}
	if _, err := repo.LatestChallenge(ctx, p); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("sign-in codes left: %v", err)
	}
	if err := repo.DeleteAccount(ctx, in.Account); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("deleting again: %v", err)
	}
	if _, err := repo.AccountByID(ctx, other.ID); err != nil {
		t.Fatalf("the other account: %v", err)
	}
}

// The renewal ledger (issue #97): a pass that gets a later end, early or
// after it ran out, leaves a row with the end it had; the first pass on a
// box, a reminder claimed and a transfer leave none.
func TestPostgres_RenewalLedger(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	now := t0
	svc, _ := service(t, repo, &now)
	acc, err := repo.EnsureAccount(ctx, usecase.Account{ID: domain.AccountID(uuid.NewString()), Phone: newPhone(), CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	b := newBox()
	credit := func() domain.Subscription {
		t.Helper()
		sub, _, err := svc.CreditPass(ctx, usecase.CreditInput{
			Account: acc.ID, BoxID: b, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "ord-" + randHex(8),
		})
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}
	type row struct {
		due, next time.Time
		renewed   bool
	}
	ledger := func() []row {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT due_at, new_expires_at, renewed_at IS NOT NULL FROM hub_licensing_renewals
			WHERE account_id = $1 ORDER BY id`, string(acc.ID))
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := []row{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.due, &r.next, &r.renewed); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}

	first := credit()
	if l := ledger(); len(l) != 0 {
		t.Fatalf("the first pass is a renewal: %v", l)
	}
	if ok, err := repo.ClaimReminder(ctx, usecase.DueReminder{Sub: first, Step: domain.ReminderBefore}, now); err != nil || !ok {
		t.Fatalf("claim reminder: %v %v", ok, err)
	}
	second := credit() // early: before the end
	now = second.ExpiresAt.Add(10 * 24 * time.Hour)
	third := credit() // late: ten days after the end
	l := ledger()
	if len(l) != 2 || !l[0].due.Equal(first.ExpiresAt) || !l[0].next.Equal(second.ExpiresAt) ||
		!l[1].due.Equal(second.ExpiresAt) || !l[1].next.Equal(third.ExpiresAt) || !l[0].renewed || !l[1].renewed {
		t.Fatalf("ledger = %+v, want the first and the second end", l)
	}
	if _, err := svc.Transfer(ctx, acc.ID, string(b), string(newBox())); err != nil {
		t.Fatal(err)
	}
	if l := ledger(); len(l) != 2 {
		t.Fatalf("a transfer is a renewal: %+v", l)
	}
}
