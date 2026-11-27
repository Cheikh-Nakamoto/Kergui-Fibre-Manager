package usecase_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/memstore"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

type notice struct {
	account     domain.AccountID
	title, body string
}

// fakeNotices is the household's app: phones phones, all reached.
type fakeNotices struct {
	mu     sync.Mutex
	got    []notice
	phones int
	err    error
}

func (n *fakeNotices) NotifyAccount(_ context.Context, a domain.AccountID, title, body string) (usecase.AppNotice, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.err != nil {
		return usecase.AppNotice{}, n.err
	}
	n.got = append(n.got, notice{a, title, body})
	return usecase.AppNotice{Phones: n.phones, Sent: n.phones}, nil
}

func (n *fakeNotices) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.got)
}

func (n *fakeNotices) last(t *testing.T) notice {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.got) == 0 {
		t.Fatal("no app notification")
	}
	return n.got[len(n.got)-1]
}

const renewURL = "https://kergui.click/compte"

// reminderFixture is a signed-in number with the app on one phone, the
// website's customer area as the renewal link, and the switch on.
func reminderFixture(t *testing.T) (*fixture, *fakeNotices, usecase.Account) {
	t.Helper()
	f := newFixture(t)
	n := &fakeNotices{phones: 1}
	f.svc.Notices = n
	f.svc.RenewURL = renewURL
	return f, n, f.signIn(t)
}

// passEnding saves a prepaid pass on box ending at end.
func (f *fixture) passEnding(t *testing.T, a domain.AccountID, b string, tier domain.Tier, end time.Time) {
	t.Helper()
	sub := domain.Subscription{Account: a, BoxID: domain.BoxID(b), Tier: tier, ExpiresAt: end, Channel: domain.ChannelPrepaid}
	if err := f.store.SaveSubscription(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) sweep(t *testing.T) int {
	t.Helper()
	n, err := f.svc.SendReminders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func reminderLogs(s *memstore.Store, b string) []memstore.ReminderLog {
	var out []memstore.ReminderLog
	for _, l := range s.Reminders {
		if string(l.BoxID) == b {
			out = append(out, l)
		}
	}
	return out
}

// noSteering is RFC-006's rule for what the app shows: nothing that says
// where or how to pay.
func noSteering(t *testing.T, text string) {
	t.Helper()
	for _, w := range []string{"http", "kergui.click", "page", "payer", "acheter", "prix", "F CFA", "Prolongez", "prenez un pass"} {
		if strings.Contains(text, w) {
			t.Errorf("app notification %q says %q", text, w)
		}
	}
}

func TestSendReminders_J3ThenJ0OnceEachByWhatsAppAndInTheApp(t *testing.T) {
	f, app, acc := reminderFixture(t)
	end := time.Date(2026, 11, 28, 15, 0, 0, 0, time.UTC)
	f.passEnding(t, acc.ID, box, domain.TierMax, end)

	f.now = end.Add(-domain.ReminderLead - time.Minute)
	if n := f.sweep(t); n != 0 {
		t.Fatalf("a minute before J-3: %d", n)
	}
	f.now = end.Add(-domain.ReminderLead)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("J-3: %d", n)
	}
	wa := f.sender.last(t)
	// One box on the account: its serial means nothing to the household,
	// so the reminder does not say it (audit M6).
	if wa.to != phone || !strings.HasPrefix(wa.text, "Kergui : votre pass Max se termine le 28/11 à 15h00.") ||
		strings.Contains(strings.TrimSuffix(wa.text, renewURL), box) ||
		!strings.HasSuffix(wa.text, renewURL) {
		t.Fatalf("J-3 WhatsApp %q", wa.text)
	}
	got := app.last(t)
	if got.account != acc.ID || got.title != "Votre pass se termine bientôt" ||
		got.body != "Votre pass Max se termine le 28/11 à 15h00." {
		t.Fatalf("J-3 app notification %+v", got)
	}
	noSteering(t, got.title+" "+got.body)

	// Idempotent: the next sweeps, the rest of the days before, send nothing.
	before := f.sender.count(phone)
	for _, at := range []time.Time{f.now.Add(10 * time.Minute), time.Date(2026, 11, 27, 19, 0, 0, 0, time.UTC)} {
		f.now = at
		if n := f.sweep(t); n != 0 {
			t.Fatalf("J-3 again at %s: %d", at, n)
		}
	}
	if f.sender.count(phone) != before || app.count() != 1 {
		t.Fatal("reminded twice for J-3")
	}

	// J0: the morning of the day it ends, once.
	f.now = time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("J0: %d", n)
	}
	if wa := f.sender.last(t).text; !strings.Contains(wa, "votre pass Max se termine aujourd'hui à 15h00.") || !strings.HasSuffix(wa, renewURL) {
		t.Fatalf("J0 WhatsApp %q", wa)
	}
	if got := app.last(t); got.title != "Votre pass se termine aujourd'hui" || !strings.Contains(got.body, "aujourd'hui à 15h00") {
		t.Fatalf("J0 app notification %+v", got)
	}
	f.now = end.Add(time.Hour)
	if n := f.sweep(t); n != 0 {
		t.Fatalf("J0 again: %d", n)
	}

	logs := reminderLogs(f.store, box)
	if len(logs) != 2 || logs[0].Step != domain.ReminderBefore || logs[1].Step != domain.ReminderDay {
		t.Fatalf("log %+v", logs)
	}
	for _, l := range logs {
		if l.WhatsApp != usecase.ReminderSent || l.Push != usecase.ReminderSent || l.PushPhones != 1 || l.PushSent != 1 || !l.ExpiresAt.Equal(end) {
			t.Errorf("log %+v", l)
		}
	}
}

func TestSendReminders_ARenewedPassIsNotRemindedOfItsOldEnd(t *testing.T) {
	f, app, acc := reminderFixture(t)
	if _, _, err := f.svc.CreditPass(context.Background(), usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "basic_1m", Provider: usecase.ProviderNabooPay, Reference: "o1"}); err != nil {
		t.Fatal(err)
	}
	f.advance(27 * 24 * time.Hour)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("J-3: %d", n)
	}
	// Renewed after J-3: the old end's J0 never comes, the new end's
	// reminders do.
	if _, _, err := f.svc.CreditPass(context.Background(), usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "basic_1m", Provider: usecase.ProviderNabooPay, Reference: "o2"}); err != nil {
		t.Fatal(err)
	}
	f.advance(3 * 24 * time.Hour)
	if n := f.sweep(t); n != 0 {
		t.Fatalf("the old end's day: %d", n)
	}
	f.advance(27 * 24 * time.Hour)
	if n := f.sweep(t); n != 1 || app.count() != 2 {
		t.Fatalf("the new end's J-3: %d, %d app notifications", n, app.count())
	}
}

func TestSendReminders_APassEndedAtNightIsToldInTheMorning(t *testing.T) {
	f, app, acc := reminderFixture(t)
	end := time.Date(2026, 11, 28, 2, 0, 0, 0, time.UTC)
	f.passEnding(t, acc.ID, box, domain.TierBasic, end)
	// The J-3 reminder, due at 02:00 three days before, waits for 09:00.
	f.now = end.Add(-domain.ReminderLead)
	if n := f.sweep(t); n != 0 {
		t.Fatalf("at night: %d", n)
	}
	f.now = time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("J-3 in the morning: %d", n)
	}
	// The day it ends, at 02:00: nothing until 09:00, then « a expiré ».
	f.now = end
	if n := f.sweep(t); n != 0 {
		t.Fatalf("J0 at night: %d", n)
	}
	f.now = time.Date(2026, 11, 28, 9, 30, 0, 0, time.UTC)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("J0 in the morning: %d", n)
	}
	wa := f.sender.last(t).text
	if !strings.Contains(wa, "votre pass Basic a pris fin aujourd'hui à 02h00 : la box est repassée en Gratuit.") ||
		!strings.Contains(wa, "Reprenez un pass ici : "+renewURL) {
		t.Fatalf("J0 WhatsApp %q", wa)
	}
	if got := app.last(t); got.title != "Votre pass est terminé" || !strings.Contains(got.body, "a pris fin aujourd'hui à 02h00") {
		t.Fatalf("J0 app notification %+v", got)
	}
	noSteering(t, app.last(t).body)
	// 20:00 and later is night again.
	f.passEnding(t, acc.ID, box2, domain.TierBasic, time.Date(2026, 11, 29, 21, 0, 0, 0, time.UTC))
	f.now = time.Date(2026, 11, 28, 20, 0, 0, 0, time.UTC)
	if n := f.sweep(t); n != 0 {
		t.Fatalf("at 20:00: %d", n)
	}
}

func TestSendReminders_TheSwitchOffSendsNothingAndLosesNothing(t *testing.T) {
	f, app, acc := reminderFixture(t)
	on := false
	f.svc.RemindersOn = func(context.Context) (bool, error) { return on, nil }
	end := time.Date(2026, 11, 28, 15, 0, 0, 0, time.UTC)
	f.passEnding(t, acc.ID, box, domain.TierMax, end)
	f.now = end.Add(-domain.ReminderLead)
	before := f.sender.count(phone)
	if n := f.sweep(t); n != 0 || f.sender.count(phone) != before || app.count() != 0 || len(f.store.Reminders) != 0 {
		t.Fatalf("switched off: %d sent", n)
	}
	on = true
	f.advance(time.Hour)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("switched back on: %d", n)
	}
	f.svc.RemindersOn = func(context.Context) (bool, error) { return false, errors.New("db down") }
	if _, err := f.svc.SendReminders(context.Background()); err == nil {
		t.Fatal("a setting that cannot be read sends anyway")
	}
}

func TestSendReminders_ATrialEndingIsRemindedUnlessTheNumberAlreadyPaid(t *testing.T) {
	f, app, acc := reminderFixture(t)
	f.trial(t, acc.ID, box)
	f.advance(4 * 24 * time.Hour) // 3 days left
	if n := f.sweep(t); n != 1 {
		t.Fatalf("trial J-3: %d", n)
	}
	wa := f.sender.last(t).text
	if !strings.Contains(wa, "votre essai de 7 jours de Max se termine le") ||
		!strings.Contains(wa, "Pour garder Max, prenez un pass ici : "+renewURL) {
		t.Fatalf("trial WhatsApp %q", wa)
	}
	if got := app.last(t); got.title != "Votre essai Max se termine bientôt" || !strings.HasPrefix(got.body, "Votre essai de 7 jours de Max se termine le") {
		t.Fatalf("trial app notification %+v", got)
	}

	// The number pays for a pass, on another box: the trial converted, its
	// J0 reminder is skipped, and the back-office says why.
	if _, _, err := f.svc.CreditPass(context.Background(), usecase.CreditInput{Account: acc.ID, BoxID: box2, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "o1", AmountXOF: 2000}); err != nil {
		t.Fatal(err)
	}
	sent, notified := f.sender.count(phone), app.count()
	f.advance(3 * 24 * time.Hour) // the day the trial ends
	if n := f.sweep(t); n != 0 || f.sender.count(phone) != sent || app.count() != notified {
		t.Fatalf("a converted trial was reminded: %d", n)
	}
	logs := reminderLogs(f.store, box)
	if len(logs) != 2 || logs[1].Step != domain.ReminderDay || logs[1].WhatsApp != usecase.ReminderSkipped ||
		logs[1].Push != usecase.ReminderSkipped || logs[1].Note != usecase.NoteTrialConverted {
		t.Fatalf("log %+v", logs)
	}
	if n := f.sweep(t); n != 0 || len(reminderLogs(f.store, box)) != 2 {
		t.Fatal("a skipped reminder is looked at again")
	}
}

func TestSendReminders_APassPaidAndWaitingForItsBoxIsARenewal(t *testing.T) {
	f, app, acc := reminderFixture(t)
	end := t0.Add(2 * 24 * time.Hour)
	f.passEnding(t, acc.ID, box, domain.TierBasic, end)
	// Paid on the website without the box's serial: it waits on the account.
	out, err := f.svc.Checkout(context.Background(), usecase.CheckoutInput{Phone: phone, PassCode: "basic_1m"})
	if err != nil {
		t.Fatal(err)
	}
	f.gw.pay(out.OrderID)
	if ok, err := f.svc.SettleOrder(context.Background(), out.OrderID); !ok || err != nil {
		t.Fatalf("settle: %v %v", ok, err)
	}
	sent := f.sender.count(phone)
	if n := f.sweep(t); n != 0 || f.sender.count(phone) != sent || app.count() != 0 {
		t.Fatalf("reminded with a pass waiting: %d", n)
	}
	if logs := reminderLogs(f.store, box); len(logs) != 1 || logs[0].Note != usecase.NoteWaitingPass {
		t.Fatalf("log %+v", logs)
	}
}

func TestSendReminders_WhatsAppIsTriedThreeTimesAndTheAppOnce(t *testing.T) {
	f, app, acc := reminderFixture(t)
	end := t0.Add(2 * 24 * time.Hour)
	f.passEnding(t, acc.ID, box, domain.TierMax, end)
	f.sender.err = errors.New("whatsapp down")
	// The app notification still goes out, and counts as reaching them.
	if n, err := f.svc.SendReminders(context.Background()); err == nil || n != 1 {
		t.Fatalf("first try: %d %v", n, err)
	}
	for i := 0; i < 4; i++ {
		f.advance(10 * time.Minute)
		_, _ = f.svc.SendReminders(context.Background())
	}
	l := reminderLogs(f.store, box)
	if len(l) != 1 || l[0].WhatsApp != usecase.ReminderFailed || l[0].Tries != domain.ReminderTries || l[0].Push != usecase.ReminderSent {
		t.Fatalf("log %+v", l)
	}
	if app.count() != 1 {
		t.Fatalf("%d app notifications, want 1", app.count())
	}
	// WhatsApp back, but the J-3 reminder was given up: no fourth try.
	f.sender.err = nil
	f.advance(10 * time.Minute)
	if n := f.sweep(t); n != 0 {
		t.Fatalf("a fourth try: %d", n)
	}
}

func TestSendReminders_RetriesWhatsAppOnlyAndRecordsTheAppChannel(t *testing.T) {
	f, app, acc := reminderFixture(t)
	f.passEnding(t, acc.ID, box, domain.TierMax, t0.Add(2*24*time.Hour))
	f.sender.err = errors.New("whatsapp down")
	_, _ = f.svc.SendReminders(context.Background())
	f.sender.err = nil
	f.advance(10 * time.Minute)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("retry: %d", n)
	}
	l := reminderLogs(f.store, box)
	if len(l) != 1 || l[0].WhatsApp != usecase.ReminderSent || l[0].Tries != 2 || l[0].Push != usecase.ReminderSent || app.count() != 1 {
		t.Fatalf("log %+v, %d app notifications", l, app.count())
	}
}

func TestSendReminders_TheAppChannelSaysWhoItReached(t *testing.T) {
	for _, tc := range []struct {
		name    string
		notices usecase.AppNotifier
		want    usecase.ReminderStatus
	}{
		{"no push on the hub", nil, usecase.ReminderOff},
		{"push off", &fakeNotices{err: usecase.ErrNoticesOff}, usecase.ReminderOff},
		{"nobody has the app", &fakeNotices{}, usecase.ReminderNone},
		{"push refused", &fakeNotices{err: errors.New("expo down")}, usecase.ReminderFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _, acc := reminderFixture(t)
			f.svc.Notices = tc.notices
			f.passEnding(t, acc.ID, box, domain.TierMax, t0.Add(2*24*time.Hour))
			if n := f.sweep(t); n != 1 {
				t.Fatalf("WhatsApp went out: %d", n)
			}
			if l := reminderLogs(f.store, box); len(l) != 1 || l[0].Push != tc.want || l[0].WhatsApp != usecase.ReminderSent {
				t.Fatalf("log %+v, want push %s", l, tc.want)
			}
		})
	}
}

// TestSendReminders_TheSerialOnlyWhenTheAccountHasSeveralBoxes: with two
// boxes, « votre pass » needs to say which one.
func TestSendReminders_TheSerialOnlyWhenTheAccountHasSeveralBoxes(t *testing.T) {
	f, app, acc := reminderFixture(t)
	end := time.Date(2026, 11, 28, 15, 0, 0, 0, time.UTC)
	f.passEnding(t, acc.ID, box, domain.TierMax, end)
	f.passEnding(t, acc.ID, box2, domain.TierBasic, end.Add(20*24*time.Hour))
	f.now = end.Add(-domain.ReminderLead)
	if n := f.sweep(t); n != 1 {
		t.Fatalf("J-3: %d", n)
	}
	if wa := f.sender.last(t).text; !strings.HasPrefix(wa, "Kergui : votre pass Max de la box "+box+" se termine le 28/11 à 15h00.") {
		t.Fatalf("WhatsApp %q", wa)
	}
	if got := app.last(t).body; got != "Votre pass Max de la box "+box+" se termine le 28/11 à 15h00." {
		t.Fatalf("app notification %q", got)
	}
}
