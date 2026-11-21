package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	licensingusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// fakePlay is Google Play for the tests: the state of each token.
type fakePlay struct {
	mu sync.Mutex
	p  map[string]licensingusecase.PlayPurchase
}

func (f *fakePlay) Purchase(_ context.Context, tok string) (licensingusecase.PlayPurchase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.p[tok]
	if !ok {
		return licensingusecase.PlayPurchase{}, licensingusecase.ErrPlayPurchaseInvalid
	}
	return p, nil
}

func (f *fakePlay) Acknowledge(_ context.Context, _, tok string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.p[tok]
	p.Acknowledged = true
	f.p[tok] = p
	return nil
}

func (f *fakePlay) set(tok, product string, exp time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.p[tok] = licensingusecase.PlayPurchase{State: licensingusecase.PlayStateActive,
		Acknowledged: f.p[tok].Acknowledged,
		LineItems:    []licensingusecase.PlayLineItem{{ProductID: product, ExpiresAt: exp}}}
}

type fakePush struct{}

func (fakePush) Verify(_ context.Context, raw string) error {
	if raw != "pubsub-token" {
		return licensingusecase.ErrPushUnauthenticated
	}
	return nil
}

func TestLicensing_PlayPurchaseRenewalAndNoReminder(t *testing.T) {
	play := &fakePlay{p: map[string]licensingusecase.PlayPurchase{}}
	clock := &movingClock{now: time.Now().UTC().Truncate(time.Second)}
	srv, app := newTestHubApp(t, WithPlay(play, fakePush{}), WithClock(clock.Now))
	notifier := consoleNotifier(t, app)
	anon := client{t: t, base: srv.URL}
	phone, box, tok := randPhone(), "ZTEG"+randHex(4), "play-"+randHex(12)

	if code, out := anon.do("POST", "/licensing/v1/otp", map[string]any{"phone": phone}); code != http.StatusAccepted {
		t.Fatalf("otp = %d %v", code, out)
	}
	msg, _ := notifier.LastTo(alertsdomain.Phone(phone))
	code, out := anon.do("POST", "/licensing/v1/otp/verify", map[string]any{"phone": phone, "code": otpCode.FindString(msg.Text)})
	if code != http.StatusOK {
		t.Fatalf("verify = %d %v", code, out)
	}
	user := client{t: t, base: srv.URL, token: str(out, "sessionToken")}

	exp := clock.Now().Add(30 * 24 * time.Hour)
	play.set(tok, "kergui_basic", exp)
	code, out = user.do("POST", "/licensing/v1/play/purchase", map[string]any{"boxId": box, "productId": "kergui_basic", "purchaseToken": tok})
	if code != http.StatusOK || str(out, "tier") != "basic" || str(out, "channel") != "play" || str(out, "expiresAt") != exp.Format(time.RFC3339) {
		t.Fatalf("play purchase = %d %v", code, out)
	}

	// The renewal arrives through Pub/Sub.
	exp2 := exp.Add(30 * 24 * time.Hour)
	play.set(tok, "kergui_basic", exp2)
	data, _ := json.Marshal(map[string]any{"packageName": "", "subscriptionNotification": map[string]any{
		"notificationType": licensingusecase.PlayRenewed, "purchaseToken": tok, "subscriptionId": "kergui_basic"}})
	body, _ := json.Marshal(map[string]any{"message": map[string]any{"data": base64.StdEncoding.EncodeToString(data)}})
	req, _ := http.NewRequest("POST", srv.URL+"/licensing/v1/webhooks/play", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer pubsub-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&ack)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || ack["outcome"] != "credited" {
		t.Fatalf("rtdn = %d %v", resp.StatusCode, ack)
	}

	// Two days before the end: Play renews by itself, no reminder.
	before, _ := notifier.LastTo(alertsdomain.Phone(phone))
	clock.advance(exp2.Sub(clock.Now()) - 2*24*time.Hour)
	if err := app.Licensing().Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after, _ := notifier.LastTo(alertsdomain.Phone(phone)); after.MessageID != before.MessageID {
		t.Fatalf("a Play subscription was reminded: %q", after.Text)
	}
	code, out = user.do("POST", "/licensing/v1/token", map[string]any{"boxId": box})
	if code != http.StatusOK || str(out, "tier") != "basic" {
		t.Fatalf("token = %d %v", code, out)
	}
}

func TestLicensing_PlayOffAnswers404(t *testing.T) {
	srv, _ := newTestHubApp(t)
	anon := client{t: t, base: srv.URL}
	if code, _ := anon.do("POST", "/licensing/v1/webhooks/play", map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("webhook with Play off = %d", code)
	}
}
