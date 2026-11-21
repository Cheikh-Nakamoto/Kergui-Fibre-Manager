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
	mu       sync.Mutex
	n        int
	requests []port.CheckoutRequest
	status   map[string]coredomain.PaymentStatus
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

func (g *fakeGateway) CancelCheckout(context.Context, string) error { return nil }

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
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); !errors.Is(err, usecase.ErrOTPInvalid) {
			t.Fatalf("spent code reused: %v", err)
		}
	})
	t.Run("expired", func(t *testing.T) {
		f := newFixture(t)
		_, _ = f.svc.RequestOTP(ctx, phone)
		code := f.sender.code(t)
		f.advance(usecase.OTPTTL)
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); !errors.Is(err, usecase.ErrOTPInvalid) {
			t.Fatalf("expired code: %v", err)
		}
	})
	t.Run("locked after max attempts", func(t *testing.T) {
		f := newFixture(t)
		_, _ = f.svc.RequestOTP(ctx, phone)
		code := f.sender.code(t)
		for i := 0; i < usecase.OTPMaxAttempts; i++ {
			_, _ = f.svc.VerifyOTP(ctx, phone, wrong(code), "")
		}
		if _, err := f.svc.VerifyOTP(ctx, phone, code, ""); !errors.Is(err, usecase.ErrOTPInvalid) {
			t.Fatalf("right code after lock-out: %v", err)
		}
	})
	t.Run("no code requested", func(t *testing.T) {
		f := newFixture(t)
		if _, err := f.svc.VerifyOTP(ctx, phone, "123456", ""); !errors.Is(err, usecase.ErrOTPInvalid) {
			t.Fatalf("err = %v", err)
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

func TestStartTrial_OncePerPhoneAndOncePerBox(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)

	if _, err := f.svc.StartTrial(ctx, acc.ID, "not a serial!"); !errors.Is(err, domain.ErrInvalidBoxID) {
		t.Fatalf("bad box: %v", err)
	}
	sub, err := f.svc.StartTrial(ctx, acc.ID, "zteg00000001")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Tier != domain.TierMax || !sub.ExpiresAt.Equal(t0.Add(7*24*time.Hour)) || sub.BoxID != box {
		t.Fatalf("trial %+v", sub)
	}
	if _, err := f.svc.StartTrial(ctx, acc.ID, box2); !errors.Is(err, domain.ErrTrialPhoneUsed) {
		t.Fatalf("same phone, other box: %v", err)
	}

	// Another number on the same box.
	other := usecase.Account{ID: "acc-other", Phone: "+221770000009", CreatedAt: t0}
	if _, err := f.store.EnsureAccount(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.StartTrial(ctx, other.ID, box); !errors.Is(err, domain.ErrTrialBoxUsed) {
		t.Fatalf("other phone, same box: %v", err)
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
	if _, err := f.svc.Checkout(ctx, usecase.CheckoutInput{Phone: phone, BoxID: "", PassCode: "max_1m"}); !errors.Is(err, domain.ErrInvalidBoxID) {
		t.Errorf("no box: %v", err)
	}
	acc, _ := f.store.AccountByPhone(ctx, phone)
	if acc.ID == "" {
		acc = f.signIn(t)
	}
	if _, err := f.svc.StartTrial(ctx, acc.ID, box); err != nil {
		t.Fatal(err)
	}
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

func TestCreditPass_IsTheOneSeamForEveryProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	in := usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "basic_1m", Provider: usecase.ProviderPlay, Reference: "play-token-1"}
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
	sub, _, err = f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderPlay, Reference: "play-token-2"})
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

	if _, err := f.svc.StartTrial(ctx, acc.ID, box); err != nil {
		t.Fatal(err)
	}
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
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_3m", Provider: usecase.ProviderPlay, Reference: "r1"}); err != nil {
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
		if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: b, PassCode: "basic_1m", Provider: usecase.ProviderPlay, Reference: ref}); err != nil {
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

func TestSendReminders_ThreeDaysBeforeExpiryOnceWithANabooPayLink(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "o1"}); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 0 {
		t.Fatalf("too early: %d %v", n, err)
	}
	f.advance(27 * 24 * time.Hour)
	before := len(f.sender.msgs)
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 1 {
		t.Fatalf("due: %d %v", n, err)
	}
	m := f.sender.last(t)
	if m.to != phone || !strings.Contains(m.text, "https://pay.test/") || !strings.Contains(m.text, box) {
		t.Fatalf("reminder %q", m.text)
	}
	if n, _ := f.svc.SendReminders(ctx); n != 0 || len(f.sender.msgs) != before+1 {
		t.Fatal("reminded twice for the same expiry")
	}
	// The link is a real checkout for the same tier, one month.
	last := f.gw.requests[len(f.gw.requests)-1]
	if last.AmountXOF != 2000 {
		t.Fatalf("reminder checkout %+v", last)
	}
	// Renewing moves the expiry, so the next one is due again later.
	if _, _, err := f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box, PassCode: "max_1m", Provider: usecase.ProviderNabooPay, Reference: "o2"}); err != nil {
		t.Fatal(err)
	}
	f.advance(30 * 24 * time.Hour)
	if n, _ := f.svc.SendReminders(ctx); n != 1 {
		t.Fatal("no reminder for the renewed expiry")
	}
}

func TestSendReminders_ReleasesTheClaimWhenWhatsAppFails(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	if _, err := f.svc.StartTrial(ctx, acc.ID, box); err != nil {
		t.Fatal(err)
	}
	f.advance(5 * 24 * time.Hour)
	f.svc.Gateway = nil // no link: the reminder still goes out
	f.sender.err = errors.New("whatsapp down")
	if n, err := f.svc.SendReminders(ctx); err == nil || n != 0 {
		t.Fatalf("send failure: %d %v", n, err)
	}
	f.sender.err = nil
	if n, err := f.svc.SendReminders(ctx); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	if strings.Contains(f.sender.last(t).text, "http") {
		t.Errorf("a link without a gateway: %q", f.sender.last(t).text)
	}
}

func TestSubscriptions_ListsEveryBoxOfTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	_, _ = f.svc.StartTrial(ctx, acc.ID, box)
	_, _, _ = f.svc.CreditPass(ctx, usecase.CreditInput{Account: acc.ID, BoxID: box2, PassCode: "basic_1m", Provider: usecase.ProviderPlay, Reference: "x"})
	list, err := f.svc.Subscriptions(ctx, acc.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
}
