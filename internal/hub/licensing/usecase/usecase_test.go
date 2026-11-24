package usecase_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	coredomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/memstore"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	phone = "+221771234567"
	box   = "ZTEG00000001"
	box2  = "ZTEG00000002"
)

var t0 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

type sent struct {
	to   alerts.Phone
	text string
}

type fakeSender struct {
	mu   sync.Mutex
	msgs []sent
	err  error
}

func (f *fakeSender) Send(_ context.Context, to alerts.Phone, m alerts.OutboundMessage) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	f.msgs = append(f.msgs, sent{to, m.Text})
	return "id", nil
}

func (f *fakeSender) last(t *testing.T) sent {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.msgs) == 0 {
		t.Fatal("nothing sent")
	}
	return f.msgs[len(f.msgs)-1]
}

func (f *fakeSender) count(to string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.msgs {
		if string(m.to) == to {
			n++
		}
	}
	return n
}

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

func (f *fakeSender) code(t *testing.T) string {
	t.Helper()
	c := sixDigits.FindString(f.last(t).text)
	if c == "" {
		t.Fatalf("no 6-digit code in %q", f.last(t).text)
	}
	return c
}

type fakeGateway struct {
	mu        sync.Mutex
	n         int
	requests  []port.CheckoutRequest
	status    map[string]coredomain.PaymentStatus
	cancelled []string
	cancelErr error
}

func (g *fakeGateway) CreateCheckout(_ context.Context, r port.CheckoutRequest) (port.CheckoutResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	g.requests = append(g.requests, r)
	id := "order-" + string(rune('0'+g.n))
	return port.CheckoutResult{OrderID: id, CheckoutURL: "https://pay.test/" + id, AmountXOF: r.AmountXOF}, nil
}

func (g *fakeGateway) TransactionStatus(_ context.Context, id string) (coredomain.PaymentStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.status[id]; ok {
		return s, nil
	}
	return coredomain.PaymentPending, nil
}

func (g *fakeGateway) CancelCheckout(_ context.Context, id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancelErr != nil {
		return g.cancelErr
	}
	g.cancelled = append(g.cancelled, id)
	return nil
}

func (g *fakeGateway) pay(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.status == nil {
		g.status = map[string]coredomain.PaymentStatus{}
	}
	g.status[id] = coredomain.PaymentPaid
}

type fixture struct {
	svc    *usecase.Service
	store  *memstore.Store
	sender *fakeSender
	gw     *fakeGateway
	now    time.Time
	pub    ed25519.PublicKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: memstore.New(), sender: &fakeSender{}, gw: &fakeGateway{}, now: t0, pub: pub}
	f.svc = &usecase.Service{
		Store: f.store, Sender: f.sender, Gateway: f.gw, SigningKey: priv,
		ReturnURL: "https://hub.test/licensing/v1/checkout/done",
		BuyURL:    "https://hub.test/licensing/v1/buy",
		Now:       func() time.Time { return f.now },
	}
	return f
}

func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }

// signIn runs the whole OTP flow and returns the account.
func (f *fixture) signIn(t *testing.T) usecase.Account {
	t.Helper()
	if _, err := f.svc.RequestOTP(context.Background(), phone); err != nil {
		t.Fatal(err)
	}
	in, err := f.svc.VerifyOTP(context.Background(), phone, f.sender.code(t), "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	return in.Account
}

// trial asks for the web trial of the signed-in number and binds it to b,
// as the purchase page and then the app do.
func (f *fixture) trial(t *testing.T, a domain.AccountID, b string) domain.Subscription {
	t.Helper()
	acc, err := f.store.AccountByID(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RequestTrial(context.Background(), string(acc.Phone)); err != nil {
		t.Fatal(err)
	}
	out, err := f.svc.BindBox(context.Background(), a, b)
	if err != nil || out.Trial != usecase.TrialStarted {
		t.Fatalf("bind: %+v %v", out, err)
	}
	return out.Subscription
}

// --- OTP and sessions ------------------------------------------------------

func TestRequestOTP_SendsSixDigitsOverWhatsAppAndStoresOnlyAHash(t *testing.T) {
	f := newFixture(t)
	out, err := f.svc.RequestOTP(context.Background(), "+221 77 123 45 67")
	if err != nil {
		t.Fatal(err)
	}
	if !out.ExpiresAt.Equal(t0.Add(usecase.OTPTTL)) {
		t.Errorf("expires %v", out.ExpiresAt)
	}
	m := f.sender.last(t)
	if m.to != phone {
		t.Errorf("sent to %q", m.to)
	}
	code := f.sender.code(t)
	c, err := f.store.LatestChallenge(context.Background(), phone)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(c.CodeHash), code) || len(c.CodeHash) != 32 {
		t.Errorf("code stored in clear or not hashed: %x", c.CodeHash)
	}
}

func TestRequestOTP_RejectsBadPhone(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.RequestOTP(context.Background(), "hello"); !errors.Is(err, alerts.ErrInvalidPhone) {
		t.Fatalf("err = %v", err)
	}
}

func TestRequestOTP_RateLimitedPerPhone(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.RequestOTP(ctx, phone)
	var rl *usecase.RateLimitError
	if !errors.Is(err, usecase.ErrOTPRateLimited) || !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		t.Fatalf("immediate resend: err = %v", err)
	}
	// Another number is not affected.
	if _, err := f.svc.RequestOTP(ctx, "+221770000001"); err != nil {
		t.Fatal(err)
	}
	// After the resend delay it works, up to the hourly cap.
	for i := 1; i < usecase.OTPPerHour; i++ {
		f.advance(usecase.OTPResendAfter)
		if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	f.advance(usecase.OTPResendAfter)
	if _, err := f.svc.RequestOTP(ctx, phone); !errors.Is(err, usecase.ErrOTPRateLimited) {
		t.Fatalf("over the hourly cap: err = %v", err)
	}
	f.advance(time.Hour)
	if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
		t.Fatalf("an hour later: %v", err)
	}
}

func TestVerifyOTP_CreatesAccountOnceAndSeveralPhonesSignIn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t)
	if first.Phone != phone || first.ID == "" {
		t.Fatalf("account %+v", first)
	}
	f.advance(usecase.OTPResendAfter)
	if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
		t.Fatal(err)
	}
	second, err := f.svc.VerifyOTP(ctx, phone, f.sender.code(t), "Tecno")
	if err != nil {
		t.Fatal(err)
	}
	if second.Account.ID != first.ID {
		t.Fatalf("a second phone got another account: %s vs %s", second.Account.ID, first.ID)
	}
	if !strings.HasPrefix(second.Token, usecase.SessionTokenPrefix) {
		t.Errorf("token %q", second.Token)
	}
	got, err := f.svc.Authenticate(ctx, second.Token)
	if err != nil || got.ID != first.ID {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
}

func TestVerifyOTP_WrongExpiredSpentAndLocked(t *testing.T) {
	ctx := context.Background()

	t.Run("wrong then right", func(t *testing.T) {
		f := newFixture(t)
		_, _ = f.svc.RequestOTP(ctx, phone)
		code := f.sender.code(t)
		if _, err := f.svc.VerifyOTP(ctx, phone, wrong(code), ""); !errors.Is(err, usecase.ErrOTPInvalid) {
			t.Fatalf("wrong code: %v", err)
		}
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); err != nil {
			t.Fatalf("right code after one miss: %v", err)
		}
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); !errors.Is(err, usecase.ErrOTPExpired) {
			t.Fatalf("spent code reused: %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		f := newFixture(t)
		_, _ = f.svc.RequestOTP(ctx, phone)
		code := f.sender.code(t)
		f.advance(usecase.OTPTTL)
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); !errors.Is(err, usecase.ErrOTPExpired) {
			t.Fatalf("expired code: %v", err)
		}
	})
	t.Run("locked after max attempts", func(t *testing.T) {
		f := newFixture(t)
		_, _ = f.svc.RequestOTP(ctx, phone)
		code := f.sender.code(t)
		for i := 1; i < usecase.OTPMaxAttempts; i++ {
			if _, err := f.svc.VerifyOTP(ctx, phone, wrong(code), ""); !errors.Is(err, usecase.ErrOTPInvalid) {
				t.Fatalf("miss %d: %v", i, err)
			}
		}
		// The last allowed miss already says the code is spent, so the
		// person knows to ask for another instead of guessing again.
		if _, err := f.svc.VerifyOTP(ctx, phone, wrong(code), ""); !errors.Is(err, usecase.ErrOTPLocked) {
			t.Fatalf("last miss: %v", err)
		}
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); !errors.Is(err, usecase.ErrOTPLocked) {
			t.Fatalf("right code after lock-out: %v", err)
		}
	})
	t.Run("a new code after a lock-out works", func(t *testing.T) {
		f := newFixture(t)
		_, _ = f.svc.RequestOTP(ctx, phone)
		code := f.sender.code(t)
		for i := 0; i < usecase.OTPMaxAttempts; i++ {
			_, _ = f.svc.VerifyOTP(ctx, phone, wrong(code), "")
		}
		f.advance(usecase.OTPResendAfter)
		if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.VerifyOTP(ctx, phone, f.sender.code(t), ""); err != nil {
			t.Fatalf("fresh code: %v", err)
		}
	})
	t.Run("no code requested", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.svc.VerifyOTP(ctx, phone, "123456", ""); !errors.Is(err, usecase.ErrOTPExpired) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("the three refusals are distinct", func(t *testing.T) {
		for _, pair := range [][2]error{
			{usecase.ErrOTPInvalid, usecase.ErrOTPExpired},
			{usecase.ErrOTPInvalid, usecase.ErrOTPLocked},
			{usecase.ErrOTPExpired, usecase.ErrOTPLocked},
		} {
			if errors.Is(pair[0], pair[1]) || errors.Is(pair[1], pair[0]) || pair[0].Error() == pair[1].Error() {
				t.Errorf("%v and %v are not distinct", pair[0], pair[1])
			}
		}
	})
}

func wrong(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestAuthenticate_RefusesUnknownExpiredAndSignedOut(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.svc.RequestOTP(ctx, phone)
	in, err := f.svc.VerifyOTP(ctx, phone, f.sender.code(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, "kgs_nope"); !errors.Is(err, usecase.ErrUnauthenticated) {
		t.Errorf("unknown: %v", err)
	}
	if err := f.svc.SignOut(ctx, in.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, in.Token); !errors.Is(err, usecase.ErrUnauthenticated) {
		t.Errorf("signed out: %v", err)
	}

	f.advance(usecase.OTPResendAfter)
	_, _ = f.svc.RequestOTP(ctx, phone)
	in, _ = f.svc.VerifyOTP(ctx, phone, f.sender.code(t), "")
	f.advance(usecase.SessionTTL)
	if _, err := f.svc.Authenticate(ctx, in.Token); !errors.Is(err, usecase.ErrUnauthenticated) {
		t.Errorf("expired: %v", err)
	}
}

// --- trial -------------------------------------------------------------------

func TestRequestTrial_WaitsOnTheNumberAndConfirmsOverWhatsApp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RequestTrial(ctx, "hello"); !errors.Is(err, alerts.ErrInvalidPhone) {
		t.Fatalf("bad phone: %v", err)
	}
	// No sign-in: the number's account is created, as for a purchase.
	tr, err := f.svc.RequestTrial(ctx, "+221 77 123 45 67")
	if err != nil {
		t.Fatal(err)
	}
	acc, err := f.store.AccountByPhone(ctx, phone)
	if err != nil || tr.Account != acc.ID || !tr.Pending() || !tr.RequestedAt.Equal(t0) {
		t.Fatalf("trial %+v, account %+v %v", tr, acc, err)
	}
	if m := f.sender.last(t); m.to != phone || !strings.Contains(m.text, "7 jours") {
		t.Errorf("confirmation %+v", m)
	}
	// Nothing runs yet: no box is known.
	if list, _ := f.svc.Subscriptions(ctx, acc.ID); len(list) != 0 {
		t.Errorf("subscriptions before binding: %+v", list)
	}
	// Once per number, pending or not.
	if _, err := f.svc.RequestTrial(ctx, phone); !errors.Is(err, domain.ErrTrialPhoneUsed) {
		t.Fatalf("second request: %v", err)
	}
}

func TestRequestTrial_StandsWhenWhatsAppIsDown(t *testing.T) {
	f := newFixture(t)
	f.sender.err = alerts.ErrNotActivated
	if _, err := f.svc.RequestTrial(context.Background(), phone); err != nil {
		t.Fatal(err)
	}
	acc, _ := f.store.AccountByPhone(context.Background(), phone)
	if _, err := f.store.PendingTrial(context.Background(), acc.ID); err != nil {
		t.Fatalf("trial lost with the message: %v", err)
	}
}

func TestBindBox_StartsTheSevenDaysAtBindingNotAtRequest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RequestTrial(ctx, phone); err != nil {
		t.Fatal(err)
	}
	f.advance(20 * 24 * time.Hour) // the app is installed weeks later
	acc := f.signIn(t)
	bound := f.now

	if _, err := f.svc.BindBox(ctx, acc.ID, "not a serial!"); !errors.Is(err, domain.ErrInvalidBoxID) {
		t.Fatalf("bad box: %v", err)
	}
	out, err := f.svc.BindBox(ctx, acc.ID, "zteg00000001")
	if err != nil || out.Trial != usecase.TrialStarted {
		t.Fatalf("bind %+v %v", out, err)
	}
	sub := out.Subscription
	if sub.Tier != domain.TierMax || sub.BoxID != box || !sub.ExpiresAt.Equal(bound.Add(7*24*time.Hour)) {
		t.Fatalf("trial %+v", sub)
	}
	ent, err := f.svc.IssueToken(ctx, acc.ID, box)
	if err != nil || ent.Tier != domain.TierMax || !ent.Claims.ExpiresAt.Equal(sub.ExpiresAt) {
		t.Fatalf("token %+v %v", ent, err)
	}

	// Binding again (another phone of the household, a restart) changes
	// nothing, and another box of the account gets no second trial.
	f.advance(time.Hour)
	again, err := f.svc.BindBox(ctx, acc.ID, box)
	if err != nil || again.Trial != usecase.TrialNone || !again.Subscription.ExpiresAt.Equal(sub.ExpiresAt) {
		t.Fatalf("rebind %+v %v", again, err)
	}
	other, err := f.svc.BindBox(ctx, acc.ID, box2)
	if err != nil || other.Trial != usecase.TrialNone || other.Subscription.Tier != domain.TierFree {
		t.Fatalf("second box %+v %v", other, err)
	}

	// Seven days later, back to Free.
	f.now = sub.ExpiresAt
	if ent, _ := f.svc.IssueToken(ctx, acc.ID, box); ent.Tier != domain.TierFree || ent.Token != "" {
		t.Fatalf("after the trial: %+v", ent)
	}
}

func TestBindBox_WithoutTrialJustBinds(t *testing.T) {
	f := newFixture(t)
	acc := f.signIn(t)
	out, err := f.svc.BindBox(context.Background(), acc.ID, box)
	if err != nil || out.Trial != usecase.TrialNone || out.Subscription.Tier != domain.TierFree || out.Subscription.BoxID != box {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestBindBox_RefusesTheTrialOnABoxThatHadOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t)
	f.trial(t, first.ID, box)

	// Another number asks for its trial on the web and signs in on the same
	// box: the binding works, the trial does not start there.
	const otherPhone = "+221770000009"
	if _, err := f.svc.RequestTrial(ctx, otherPhone); err != nil {
		t.Fatal(err)
	}
	other, _ := f.store.AccountByPhone(ctx, otherPhone)
	out, err := f.svc.BindBox(ctx, other.ID, box)
	if err != nil || out.Trial != usecase.TrialBoxUsed || out.Subscription.Tier != domain.TierFree {
		t.Fatalf("bind on a trialled box: %+v %v", out, err)
	}
	if ent, _ := f.svc.IssueToken(ctx, other.ID, box); ent.Tier != domain.TierFree {
		t.Fatalf("token after a refused trial: %+v", ent)
	}
	// The trial keeps waiting, and starts on a box that never had one.
	out, err = f.svc.BindBox(ctx, other.ID, box2)
	if err != nil || out.Trial != usecase.TrialStarted || out.Subscription.Tier != domain.TierMax {
		t.Fatalf("bind on a fresh box: %+v %v", out, err)
	}
}

func TestBindBox_UnknownAccount(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.BindBox(context.Background(), "nobody", box); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

// --- checkout, webhook, credit ----------------------------------------------

func TestCheckout_CreatesNabooPayOrderForThePhoneAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: "+221 77 123 45 67", BoxID: box, PassCode: "max_3m"})
	if err != nil {
		t.Fatal(err)
	}
	if out.CheckoutURL == "" || out.OrderID == "" || out.AmountXOF != 5700 || out.Pass.Code != "max_3m" {
		t.Fatalf("checkout %+v", out)
	}
	req := f.gw.requests[0]
	if req.AmountXOF != 5700 || req.Phone != phone || !strings.HasPrefix(req.SuccessURL, "https://hub.test/") {
		t.Errorf("gateway request %+v", req)
	}
	acc, err := f.store.AccountByPhone(ctx, phone)
	if err != nil {
		t.Fatal("the purchase did not create the phone's account")
	}
	p, err := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, out.OrderID)
	if err != nil || p.Status != usecase.PaymentPending || p.Account != acc.ID || p.BoxID != box {
		t.Fatalf("payment %+v %v", p, err)
	}
}

func TestCheckout_Refusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "max_trial"}); !errors.Is(err, domain.ErrUnknownPass) {
		t.Errorf("trial is not for sale: %v", err)
	}
	if _, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: "not a serial!", PassCode: "max_1m"}); !errors.Is(err, domain.ErrInvalidBoxID) {
		t.Errorf("bad box: %v", err)
	}
	acc, _ := f.store.AccountByPhone(ctx, phone)
	if acc.ID == "" {
		acc = f.signIn(t)
	}
	f.trial(t, acc.ID, box)
	if _, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "basic_1m"}); !errors.Is(err, domain.ErrMaxActive) {
		t.Errorf("basic while max runs: %v", err)
	}
	f.svc.Gateway = nil
	if _, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "max_1m"}); !errors.Is(err, usecase.ErrPaymentsOff) {
		t.Errorf("no gateway: %v", err)
	}
}

func TestSettleOrder_CreditsOnceAndOnlyWhenNabooPaySaysPaid(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "max_1m"})
	if err != nil {
		t.Fatal(err)
	}
	acc, _ := f.store.AccountByPhone(ctx, phone)

	// The webhook arrives but NabooPay still says pending: nothing moves.
	if ok, err := f.svc.SettleOrder(ctx, out.OrderID); err != nil || ok {
		t.Fatalf("pending: %v %v", ok, err)
	}
	if _, err := f.store.Subscription(ctx, acc.ID, box); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal("credited before payment")
	}

	f.gw.pay(out.OrderID)
	for i := 0; i < 3; i++ { // NabooPay retries its webhooks
		if _, err := f.svc.SettleOrder(ctx, out.OrderID); err != nil {
			t.Fatal(err)
		}
	}
	sub, err := f.store.Subscription(ctx, acc.ID, box)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Tier != domain.TierMax || !sub.ExpiresAt.Equal(t0.Add(30*24*time.Hour)) {
		t.Fatalf("credited %+v (more than once?)", sub)
	}
	p, _ := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, out.OrderID)
	if p.Status != usecase.PaymentCredited {
		t.Fatalf("payment %s", p.Status)
	}
	if ok, err := f.svc.SettleOrder(ctx, "unknown-order"); err != nil || ok {
		t.Fatalf("unknown order: %v %v", ok, err)
	}
}

// A pass bought on the web page with no box serial waits on the number's
// account, as the trial does, and is bound to the first box the app binds:
// its days start then, not at the payment.
func TestCheckout_WithoutABoxThePassWaitsForTheFirstBinding(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: "  ", PassCode: "max_3m"})
	if err != nil {
		t.Fatal(err)
	}
	if desc := f.gw.requests[0].Description; strings.Contains(desc, "Box") {
		t.Errorf("checkout description names a box: %q", desc)
	}
	acc, _ := f.store.AccountByPhone(ctx, phone)
	p, err := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, out.OrderID)
	if err != nil || p.BoxID != "" || p.Status != usecase.PaymentPending || p.Account != acc.ID {
		t.Fatalf("payment %+v %v", p, err)
	}

	// Paid: the webhook (and its replays) credit the account once, and the
	// number is told over WhatsApp that the pass waits for it.
	f.gw.pay(out.OrderID)
	credited := 0
	for i := 0; i < 3; i++ {
		ok, err := f.svc.SettleOrder(ctx, out.OrderID)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			credited++
		}
	}
	if credited != 1 {
		t.Fatalf("credited %d times", credited)
	}
	if subs, _ := f.store.ListSubscriptions(ctx, acc.ID); len(subs) != 0 {
		t.Fatalf("a box got the pass before any binding: %+v", subs)
	}
	if n := f.sender.count(phone); n != 1 {
		t.Fatalf("%d confirmations sent", n)
	}
	if msg := f.sender.last(t); msg.to != phone || !strings.Contains(msg.text, "Max") || !strings.Contains(msg.text, "3 mois") ||
		!strings.Contains(msg.text, "Connectez-vous") {
		t.Fatalf("confirmation %+v", msg)
	}

	// The app signs in on a box weeks later: the 90 days start there.
	f.advance(20 * 24 * time.Hour)
	bound := f.now
	b, err := f.svc.BindBox(ctx, acc.ID, box)
	if err != nil || b.Subscription.Tier != domain.TierMax || !b.Subscription.ExpiresAt.Equal(bound.Add(90*24*time.Hour)) {
		t.Fatalf("bind %+v %v", b, err)
	}
	ent, err := f.svc.IssueToken(ctx, acc.ID, box)
	// The token itself is short-lived; the app refreshes it while the pass runs.
	if err != nil || ent.Tier != domain.TierMax || !ent.Claims.ExpiresAt.Equal(bound.Add(domain.TokenMaxLifetime)) {
		t.Fatalf("token %+v %v", ent, err)
	}
	p, _ = f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, out.OrderID)
	if p.BoxID != box || p.Status != usecase.PaymentCredited {
		t.Fatalf("payment after binding %+v", p)
	}

	// Bound once: binding again, or another box, adds nothing; a late
	// replay of the webhook neither.
	f.advance(time.Hour)
	if again, _ := f.svc.BindBox(ctx, acc.ID, box); !again.Subscription.ExpiresAt.Equal(bound.Add(90 * 24 * time.Hour)) {
		t.Fatalf("rebind %+v", again)
	}
	if other, _ := f.svc.BindBox(ctx, acc.ID, box2); other.Subscription.Tier != domain.TierFree {
		t.Fatalf("second box %+v", other)
	}
	if ok, err := f.svc.SettleOrder(ctx, out.OrderID); ok || err != nil {
		t.Fatalf("late replay: %v %v", ok, err)
	}
}

// Two passes waiting, plus the trial: the binding applies all of them, in
// the order they were paid, by the domain's rules.
func TestBindBox_AppliesTheTrialAndEveryWaitingPass(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.RequestTrial(ctx, phone); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"max_1m", "max_12m"} {
		out, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, PassCode: code})
		if err != nil {
			t.Fatal(err)
		}
		f.gw.pay(out.OrderID)
		if ok, err := f.svc.SettleOrder(ctx, out.OrderID); !ok || err != nil {
			t.Fatalf("settle %s: %v %v", code, ok, err)
		}
		f.advance(time.Minute)
	}
	acc := f.signIn(t)
	out, err := f.svc.BindBox(ctx, acc.ID, box)
	if err != nil || out.Trial != usecase.TrialStarted {
		t.Fatalf("bind %+v %v", out, err)
	}
	want := f.now.Add((7 + 30 + 365) * 24 * time.Hour)
	if out.Subscription.Tier != domain.TierMax || !out.Subscription.ExpiresAt.Equal(want) {
		t.Fatalf("sub %+v, want expiry %v", out.Subscription, want)
	}
}

// A box that already had a trial refuses the trial, not the passes.
func TestBindBox_BindsWaitingPassesEvenWhenTheTrialIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.signIn(t)
	f.trial(t, first.ID, box)

	const otherPhone = "+221770000009"
	if _, err := f.svc.RequestTrial(ctx, otherPhone); err != nil {
		t.Fatal(err)
	}
	co, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: otherPhone, PassCode: "basic_1m"})
	if err != nil {
		t.Fatal(err)
	}
	f.gw.pay(co.OrderID)
	if _, err := f.svc.SettleOrder(ctx, co.OrderID); err != nil {
		t.Fatal(err)
	}
	other, _ := f.store.AccountByPhone(ctx, otherPhone)
	out, err := f.svc.BindBox(ctx, other.ID, box)
	if err != nil || out.Trial != usecase.TrialBoxUsed || out.Subscription.Tier != domain.TierBasic ||
		!out.Subscription.ExpiresAt.Equal(f.now.Add(30*24*time.Hour)) {
		t.Fatalf("bind %+v %v", out, err)
	}
}

// A pass for a box is credited at once, and WhatsApp says until when; a
// confirmation that cannot go out does not undo the credit.
func TestSettleOrder_ConfirmsTheCreditOverWhatsApp(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "basic_12m"})
	if err != nil {
		t.Fatal(err)
	}
	f.gw.pay(out.OrderID)
	if ok, err := f.svc.SettleOrder(ctx, out.OrderID); !ok || err != nil {
		t.Fatalf("settle %v %v", ok, err)
	}
	msg := f.sender.last(t)
	exp := t0.Add(365 * 24 * time.Hour).Format("02/01/2006")
	if msg.to != phone || !strings.Contains(msg.text, "Basic") || !strings.Contains(msg.text, "12 mois") ||
		!strings.Contains(msg.text, box) || !strings.Contains(msg.text, exp) {
		t.Fatalf("confirmation %+v (want expiry %s)", msg, exp)
	}
	if _, err := f.svc.SettleOrder(ctx, out.OrderID); err != nil {
		t.Fatal(err)
	}
	if n := f.sender.count(phone); n != 1 {
		t.Fatalf("%d confirmations for one payment", n)
	}

	f.sender.err = errors.New("whatsapp down")
	out, _ = f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box2, PassCode: "max_1m"})
	f.gw.pay(out.OrderID)
	if ok, err := f.svc.SettleOrder(ctx, out.OrderID); !ok || err != nil {
		t.Fatalf("settle with WhatsApp down: %v %v", ok, err)
	}
	acc, _ := f.store.AccountByPhone(ctx, phone)
	if sub, err := f.store.Subscription(ctx, acc.ID, box2); err != nil || sub.Tier != domain.TierMax {
		t.Fatalf("credit lost with the message: %+v %v", sub, err)
	}
}

func TestSettlePending_IsTheSafetyNetForMissedWebhooks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, _ := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "basic_1m"})
	_, _ = f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box2, PassCode: "basic_1m"})
	f.gw.pay(a.OrderID)
	n, err := f.svc.SettlePending(ctx)
	if err != nil || n != 1 {
		t.Fatalf("settled %d, %v", n, err)
	}
}

func TestPurgeStalePending_DeletesOrdersPendingForMoreThanTheTTL(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	old, _ := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "basic_1m"})
	paid, _ := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box2, PassCode: "basic_1m"})
	f.advance(2 * time.Minute)
	fresh, _ := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: box, PassCode: "max_1m"})
	f.gw.pay(paid.OrderID) // paid at the last moment: credited, never deleted
	f.advance(90 * time.Second)

	n, err := f.svc.PurgeStalePending(ctx, 3*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
	if len(f.gw.cancelled) != 1 || f.gw.cancelled[0] != old.OrderID {
		t.Fatalf("cancelled at NabooPay: %v, want only %s", f.gw.cancelled, old.OrderID)
	}
	if _, err := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, old.OrderID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("stale order still in the base: %v", err)
	}
	if p, err := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, paid.OrderID); err != nil || p.Status != usecase.PaymentCredited {
		t.Fatalf("paid order: %+v %v", p, err)
	}
	if p, err := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, fresh.OrderID); err != nil || p.Status != usecase.PaymentPending {
		t.Fatalf("fresh order: %+v %v", p, err)
	}
	// A webhook for the deleted order later is ignored.
	if credited, err := f.svc.SettleOrder(ctx, old.OrderID); err != nil || credited {
		t.Fatalf("late webhook: %v %v", credited, err)
	}

	// NabooPay refusing the delete keeps our row, for the next round.
	f.advance(5 * time.Minute)
	f.gw.cancelErr = errors.New("naboopay: 500")
	if n, err := f.svc.PurgeStalePending(ctx, 3*time.Minute); err == nil || n != 0 {
		t.Fatalf("purge with NabooPay down: %d, %v", n, err)
	}
	if p, err := f.store.PaymentByRef(ctx, usecase.ProviderNabooPay, fresh.OrderID); err != nil || p.Status != usecase.PaymentPending {
		t.Fatalf("fresh order after a failed delete: %+v %v", p, err)
	}
	f.gw.cancelErr = nil
	if n, err := f.svc.PurgeStalePending(ctx, 3*time.Minute); err != nil || n != 1 {
		t.Fatalf("retry: %d, %v", n, err)
	}
}

func TestCreditPass_IsTheOneSeamForEveryProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	in := usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "basic_1m", Provider: usecase.ProviderNabooPay, Reference: "order-1"}
	sub, already, err := f.svc.CreditPass(ctx, in)
	if err != nil || already {
		t.Fatalf("first credit: %v %v", already, err)
	}
	if sub.Tier != domain.TierBasic || !sub.ExpiresAt.Equal(t0.Add(30*24*time.Hour)) {
		t.Fatalf("sub %+v", sub)
	}
	if _, already, err = f.svc.CreditPass(ctx, in); err != nil || !already {
		t.Fatalf("replay: already=%v %v", already, err)
	}
	// The same reference for another box is not the same purchase.
	in.BoxID = box2
	if _, _, err := f.svc.CreditPass(ctx, in); !errors.Is(err, usecase.ErrPaymentMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	// Upgrade mid-pass converts the remaining Basic days (domain rule).
	f.advance(10 * 24 * time.Hour)
	sub, _, err = f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "order-2"})
	if err != nil {
		t.Fatal(err)
	}
	want := f.now.Add(domain.ConvertBasicToMax(20*24*time.Hour) + 30*24*time.Hour)
	if sub.Tier != domain.TierMax || !sub.ExpiresAt.Equal(want) {
		t.Fatalf("upgrade %+v, want expiry %v", sub, want)
	}
}

// --- token -------------------------------------------------------------------

func TestIssueToken_SignsWhatTheAppVerifiesOffline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)

	ent, err := f.svc.IssueToken(ctx, acc.ID, box)
	if err != nil || ent.Token != "" || ent.Tier != domain.TierFree {
		t.Fatalf("no pass: %+v %v", ent, err)
	}
	if _, err := f.svc.IssueToken(ctx, acc.ID, "bad box"); !errors.Is(err, domain.ErrInvalidBoxID) {
		t.Fatalf("bad box: %v", err)
	}

	f.trial(t, acc.ID, box)
	ent, err = f.svc.IssueToken(ctx, acc.ID, box)
	if err != nil {
		t.Fatal(err)
	}
	c, inGrace, err := domain.Verify(f.pub, ent.Token, f.now)
	if err != nil || inGrace {
		t.Fatalf("verify: %v grace=%v", err, inGrace)
	}
	if c.Account != acc.ID || c.BoxID != box || c.Tier != domain.TierMax || !c.ExpiresAt.Equal(t0.Add(7*24*time.Hour)) {
		t.Fatalf("claims %+v", c)
	}
	if !f.svc.PublicKey().Equal(f.pub) {
		t.Fatal("public key does not match the signing key")
	}
}

// --- transfer ----------------------------------------------------------------

func TestTransfer_MovesThePassAtMostTwicePer30Days(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_3m", Provider: usecase.ProviderNabooPay, Reference: "r1"}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Subscription(ctx, acc.ID, box)

	sub, err := f.svc.Transfer(ctx, acc.ID, box, box2)
	if err != nil {
		t.Fatal(err)
	}
	if sub.BoxID != box2 || sub.Tier != before.Tier || !sub.ExpiresAt.Equal(before.ExpiresAt) {
		t.Fatalf("moved %+v from %+v", sub, before)
	}
	if _, err := f.store.Subscription(ctx, acc.ID, box); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatal("the pass stayed on the old box too")
	}
	if _, err := f.svc.Transfer(ctx, acc.ID, box2, box); err != nil {
		t.Fatalf("second transfer: %v", err)
	}
	if _, err := f.svc.Transfer(ctx, acc.ID, box, box2); !errors.Is(err, domain.ErrTransferLimit) {
		t.Fatalf("third transfer: %v", err)
	}
	f.advance(domain.TransferWindow)
	if _, err := f.svc.Transfer(ctx, acc.ID, box, box2); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	if n := len(f.store.Transfers()); n != 3 {
		t.Fatalf("%d transfers logged", n)
	}
}

func TestTransfer_Refusals(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, err := f.svc.Transfer(ctx, acc.ID, box, box2); !errors.Is(err, domain.ErrNoActivePass) {
		t.Fatalf("nothing to move: %v", err)
	}
	for _, ref := range []string{"a", "b"} {
		b := domain.BoxID(box)
		if ref == "b" {
			b = box2
		}
		if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: b, PassCode: "basic_1m", Provider: usecase.ProviderNabooPay, Reference: ref}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Transfer(ctx, acc.ID, box, box2); !errors.Is(err, usecase.ErrTargetActive) {
		t.Fatalf("onto an active pass: %v", err)
	}
	if _, err := f.svc.Transfer(ctx, acc.ID, box, box); !errors.Is(err, domain.ErrSameBox) {
		t.Fatalf("same box: %v", err)
	}
}

// --- reminders ---------------------------------------------------------------

func TestSendReminders_ThreeDaysBeforeExpiryOnceWithALinkToThePurchasePage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "o1"}); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 0 {
		t.Fatalf("too early: %d %v", n, err)
	}
	f.advance(27*24*time.Hour - time.Minute)
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 0 {
		t.Fatalf("a minute before J-3: %d %v", n, err)
	}
	f.advance(time.Minute)
	before, checkouts := len(f.sender.msgs), len(f.gw.requests)
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 1 {
		t.Fatalf("due: %d %v", n, err)
	}
	m := f.sender.last(t)
	// The link is the hub's purchase page with the box and the same tier
	// for a month already picked: the person chooses there, and no
	// NabooPay order is opened for a reminder that may be ignored.
	link := "https://hub.test/licensing/v1/buy?box=" + box + "&pass=max_1m"
	if m.to != phone || !strings.Contains(m.text, link) || !strings.Contains(m.text, box) {
		t.Fatalf("reminder %q", m.text)
	}
	if strings.Contains(m.text, "application") {
		t.Fatalf("the app sells nothing (RFC-006): %q", m.text)
	}
	if len(f.gw.requests) != checkouts {
		t.Fatal("a reminder opened a NabooPay order")
	}
	if n, _ := f.svc.SendReminders(ctx); n != 0 || len(f.sender.msgs) != before+1 {
		t.Fatal("reminded twice for the same expiry")
	}
	// Renewing moves the expiry, so the next one is due again later.
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "o2"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.svc.SendReminders(ctx); n != 0 {
		t.Fatal("reminded right after the renewal")
	}
	f.advance(30 * 24 * time.Hour)
	if n, _ := f.svc.SendReminders(ctx); n != 1 {
		t.Fatal("no reminder for the renewed expiry")
	}
}

func TestSendReminders_NoReminderForAPlaySubscription(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	exp := f.now.Add(2 * 24 * time.Hour)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "kergui_max", Provider: usecase.ProviderPlay, Reference: "tok", ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 0 {
		t.Fatalf("Play renews by itself: %d %v", n, err)
	}
}

func TestSendReminders_ReleasesTheClaimWhenWhatsAppFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	f.trial(t, acc.ID, box)
	f.advance(5 * 24 * time.Hour)
	f.svc.BuyURL = "" // no page to link to: the reminder still goes out
	f.sender.err = errors.New("whatsapp down")
	if n, err := f.svc.SendReminders(ctx); err == nil || n != 0 {
		t.Fatalf("send failure: %d %v", n, err)
	}
	f.sender.err = nil
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if text := f.sender.last(t).text; strings.Contains(text, "http") || strings.Contains(text, "application") {
		t.Errorf("a link without a page, or the app named to pay: %q", text)
	}
}

func TestSubscriptions_ListsEveryBoxOfTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	f.trial(t, acc.ID, box)
	_, _, _ = f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box2, PassCode: "basic_1m", Provider: usecase.ProviderNabooPay, Reference: "x"})
	list, err := f.svc.Subscriptions(ctx, acc.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
}
