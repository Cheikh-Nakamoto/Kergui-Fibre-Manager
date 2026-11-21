package handler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

const (
	playPkg   = "sn.kergui.app"
	pushToken = "pubsub-oidc-token"
)

type play struct {
	mu        sync.Mutex
	purchases map[string]usecase.PlayPurchase
	down      bool
	acks      int
}

func (p *play) Purchase(_ context.Context, token string) (usecase.PlayPurchase, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return usecase.PlayPurchase{}, usecase.ErrPlayUnavailable
	}
	got, ok := p.purchases[token]
	if !ok {
		return usecase.PlayPurchase{}, usecase.ErrPlayPurchaseInvalid
	}
	return got, nil
}

func (p *play) Acknowledge(context.Context, string, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acks++
	return nil
}

func (p *play) set(token, state, product string, exp time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.purchases[token] = usecase.PlayPurchase{State: usecase.PlayState(state), Acknowledged: p.acks > 0,
		LineItems: []usecase.PlayLineItem{{ProductID: product, ExpiresAt: exp}}}
}

// push accepts the one OIDC token the test sends.
type push struct{ down bool }

func (p push) Verify(_ context.Context, raw string) error {
	if p.down {
		return errors.New("JWKS unreachable")
	}
	if raw != pushToken {
		return usecase.ErrPushUnauthenticated
	}
	return nil
}

func newPlayEnv(t *testing.T, pv usecase.PushVerifier) (*env, *play) {
	g := &play{purchases: map[string]usecase.PlayPurchase{}}
	e := newEnvWith(t, func(h *handler.Handler) {
		h.Svc.Play, h.Svc.PlayPackage = g, playPkg
		h.PlayPush = pv
	})
	return e, g
}

func (e *env) rtdn(bearer string, body []byte) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/licensing/v1/webhooks/play", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func pubsub(t *testing.T, notification any) []byte {
	t.Helper()
	data, _ := json.Marshal(notification)
	b, _ := json.Marshal(map[string]any{
		"message":      map[string]any{"data": base64.StdEncoding.EncodeToString(data), "messageId": "1"},
		"subscription": "projects/kergui/subscriptions/play-rtdn",
	})
	return b
}

func subNotification(token string, typ int) map[string]any {
	return map[string]any{
		"version": "1.0", "packageName": playPkg, "eventTimeMillis": "1795000000000",
		"subscriptionNotification": map[string]any{
			"version": "1.0", "notificationType": typ, "purchaseToken": token, "subscriptionId": "kergui_max",
		},
	}
}

func TestPlayPurchaseEndpoint(t *testing.T) {
	e, g := newPlayEnv(t, push{})
	tok := e.signIn()
	exp := e.now.Add(30 * 24 * time.Hour).Truncate(time.Second)
	g.set("tok-1", string(usecase.PlayStateActive), "kergui_max", exp)

	st, body, _ := e.do("POST", "/licensing/v1/play/purchase", tok,
		map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-1"})
	if st != http.StatusOK || body["tier"] != "max" || body["effectiveTier"] != "max" || body["active"] != true ||
		body["channel"] != "play" || body["boxId"] != box || body["expiresAt"] != exp.Format(time.RFC3339) {
		t.Fatalf("purchase = %d %v", st, body)
	}
	// The same shape as the subscriptions list.
	_, list, _ := e.do("GET", "/licensing/v1/subscriptions", tok, nil)
	if subs, _ := list["subscriptions"].([]any); len(subs) != 1 || subs[0].(map[string]any)["channel"] != "play" {
		t.Fatalf("list %v", list)
	}

	g.set("tok-pending", string(usecase.PlayStatePending), "kergui_max", exp)
	g.set("tok-expired", string(usecase.PlayStateExpired), "kergui_max", e.now.Add(-time.Hour))
	for name, tc := range map[string]struct {
		body   map[string]any
		status int
		code   string
	}{
		"unknown product": {map[string]any{"boxId": box, "productId": "kergui_pro", "purchaseToken": "tok-1"}, 400, handler.CodeUnknownProduct},
		"bad box":         {map[string]any{"boxId": "x y", "productId": "kergui_max", "purchaseToken": "tok-1"}, 400, handler.CodeInvalidBox},
		"unknown token":   {map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "nope"}, 400, handler.CodePlayInvalid},
		"pending":         {map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-pending"}, 409, handler.CodePlayPending},
		"expired":         {map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-expired"}, 409, handler.CodePlayInactive},
		"other box":       {map[string]any{"boxId": box2, "productId": "kergui_max", "purchaseToken": "tok-1"}, 409, handler.CodePlayTokenBound},
		"unknown field":   {map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-1", "x": 1}, 400, "BAD_REQUEST"},
	} {
		st, body, _ := e.do("POST", "/licensing/v1/play/purchase", tok, tc.body)
		if st != tc.status || errCode(body) != tc.code {
			t.Errorf("%s: %d %v, want %d %s", name, st, body, tc.status, tc.code)
		}
	}
	g.mu.Lock()
	g.down = true
	g.mu.Unlock()
	if st, body, _ := e.do("POST", "/licensing/v1/play/purchase", tok,
		map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-1"}); st != http.StatusBadGateway || errCode(body) != handler.CodePlayDown {
		t.Errorf("Google down: %d %v", st, body)
	}
	if st, _, _ := e.do("POST", "/licensing/v1/play/purchase", "", map[string]any{}); st != http.StatusUnauthorized {
		t.Errorf("no session: %d", st)
	}
}

func TestPlayWebhook(t *testing.T) {
	e, g := newPlayEnv(t, push{})
	tok := e.signIn()
	exp := e.now.Add(30 * 24 * time.Hour)
	g.set("tok-1", string(usecase.PlayStateActive), "kergui_max", exp)
	if st, body, _ := e.do("POST", "/licensing/v1/play/purchase", tok,
		map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-1"}); st != http.StatusOK {
		t.Fatalf("purchase %d %v", st, body)
	}

	// Unauthenticated pushes are refused before anything is read.
	if st, _ := e.rtdn("", pubsub(t, subNotification("tok-1", usecase.PlayRenewed))); st != http.StatusUnauthorized {
		t.Fatalf("no token: %d", st)
	}
	if st, _ := e.rtdn("forged", pubsub(t, subNotification("tok-1", usecase.PlayRenewed))); st != http.StatusUnauthorized {
		t.Fatalf("forged token: %d", st)
	}

	// Renewed: re-asked from Google, credited.
	exp2 := exp.Add(30 * 24 * time.Hour)
	g.set("tok-1", string(usecase.PlayStateActive), "kergui_max", exp2)
	st, body := e.rtdn(pushToken, pubsub(t, subNotification("tok-1", usecase.PlayRenewed)))
	if st != http.StatusOK || body["outcome"] != "credited" {
		t.Fatalf("renewed: %d %v", st, body)
	}
	_, list, _ := e.do("GET", "/licensing/v1/subscriptions", tok, nil)
	if sub := list["subscriptions"].([]any)[0].(map[string]any); sub["expiresAt"] != exp2.Format(time.RFC3339) {
		t.Fatalf("after renewal %v", sub)
	}

	// Revoked: Google says expired.
	g.set("tok-1", string(usecase.PlayStateExpired), "kergui_max", e.now)
	if st, body = e.rtdn(pushToken, pubsub(t, subNotification("tok-1", usecase.PlayRevoked))); st != http.StatusOK || body["outcome"] != "ended" {
		t.Fatalf("revoked: %d %v", st, body)
	}

	// Things to acknowledge and forget: a test notification, garbage.
	for name, b := range map[string][]byte{
		"test":       pubsub(t, map[string]any{"version": "1.0", "packageName": playPkg, "testNotification": map[string]any{"version": "1.0"}}),
		"not base64": []byte(`{"message":{"data":"%%%"}}`),
		"not json":   []byte(`nope`),
		"one-time":   pubsub(t, map[string]any{"packageName": playPkg, "oneTimeProductNotification": map[string]any{"purchaseToken": "x"}}),
		"unknown":    pubsub(t, subNotification("tok-unknown", usecase.PlayPurchased)),
		"other app":  pubsub(t, map[string]any{"packageName": "com.other", "subscriptionNotification": map[string]any{"purchaseToken": "tok-1"}}),
	} {
		if st, body := e.rtdn(pushToken, b); st != http.StatusOK || body["outcome"] != "ignored" {
			t.Errorf("%s: %d %v", name, st, body)
		}
	}

	// Google down: answered 503 so Pub/Sub delivers it again.
	g.mu.Lock()
	g.down = true
	g.mu.Unlock()
	if st, body := e.rtdn(pushToken, pubsub(t, subNotification("tok-1", usecase.PlayRenewed))); st != http.StatusServiceUnavailable || errCode(body) != handler.CodePlayDown {
		t.Fatalf("Google down: %d %v", st, body)
	}
}

func TestPlayWebhook_KeysUnreachableIsRetried(t *testing.T) {
	e, _ := newPlayEnv(t, push{down: true})
	if st, _ := e.rtdn(pushToken, pubsub(t, subNotification("tok-1", usecase.PlayRenewed))); st != http.StatusServiceUnavailable {
		t.Fatalf("JWKS down: %d", st)
	}
}

func TestPlayOff(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn()
	st, body, _ := e.do("POST", "/licensing/v1/play/purchase", tok,
		map[string]any{"boxId": box, "productId": "kergui_max", "purchaseToken": "tok-1"})
	if st != http.StatusNotFound || errCode(body) != handler.CodePlayOff {
		t.Fatalf("purchase with Play off: %d %v", st, body)
	}
	if st, _ := e.rtdn(pushToken, pubsub(t, subNotification("tok-1", usecase.PlayRenewed))); st != http.StatusNotFound {
		t.Fatalf("webhook with Play off: %d", st)
	}
	// Play configured but no push authentication: the webhook stays off.
	e2, _ := newPlayEnv(t, nil)
	if st, _ := e2.rtdn(pushToken, pubsub(t, subNotification("tok-1", usecase.PlayRenewed))); st != http.StatusNotFound {
		t.Fatalf("webhook without push auth: %d", st)
	}
}
