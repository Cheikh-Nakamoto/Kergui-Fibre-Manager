package googleplay_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/googleplay"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

const (
	pkg      = "sn.kergui.app"
	saEmail  = "hub@kergui-play.iam.gserviceaccount.com"
	goodTok  = "tok.good-1"
	scopeAPI = "https://www.googleapis.com/auth/androidpublisher"
)

var testKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

func serviceAccountJSON(t *testing.T, tokenURI string) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": saEmail, "private_key_id": "kid-1",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":   tokenURI,
	})
	return b
}

// fakeGoogle is the OAuth token endpoint and the Play Developer API.
type fakeGoogle struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	grants   int
	acks     []string
	purchase map[string]string // token -> subscriptionsv2 JSON
	status   int               // forced status for the API, 0 = normal
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{t: t, purchase: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		f.token(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer access-1" {
		http.Error(w, `{"error":{"code":401}}`, http.StatusUnauthorized)
		return
	}
	if f.status != 0 {
		http.Error(w, `{"error":{}}`, f.status)
		return
	}
	const base = "/androidpublisher/v3/applications/" + pkg + "/purchases/"
	switch {
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, base+"subscriptionsv2/tokens/"):
		body, ok := f.purchase[strings.TrimPrefix(r.URL.Path, base+"subscriptionsv2/tokens/")]
		if !ok {
			http.Error(w, `{"error":{"code":404}}`, http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(body))
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, base+"subscriptions/") && strings.HasSuffix(r.URL.Path, ":acknowledge"):
		f.acks = append(f.acks, strings.TrimPrefix(r.URL.Path, base+"subscriptions/"))
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusTeapot)
	}
}

// token checks the JWT bearer grant the way Google does: RS256 by the
// service account's key, for the token URI, with the androidpublisher scope.
func (f *fakeGoogle) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		http.Error(w, "bad grant", http.StatusBadRequest)
		return
	}
	parts := strings.Split(r.PostForm.Get("assertion"), ".")
	if len(parts) != 3 {
		http.Error(w, "bad assertion", http.StatusBadRequest)
		return
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var hdr struct{ Alg, Kid string }
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(h, &hdr)
	var c struct {
		Iss, Scope, Aud string
		Iat, Exp        int64
	}
	p, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(p, &c)
	if hdr.Alg != "RS256" || hdr.Kid != "kid-1" || c.Iss != saEmail || c.Scope != scopeAPI ||
		c.Aud != f.srv.URL+"/token" || c.Exp-c.Iat != 3600 {
		http.Error(w, "bad claims", http.StatusUnauthorized)
		return
	}
	f.grants++
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"access_token":"access-1","expires_in":3599,"token_type":"Bearer"}`))
}

func newClient(t *testing.T, f *fakeGoogle, now *time.Time) *googleplay.Client {
	t.Helper()
	sa, err := googleplay.ParseServiceAccount(serviceAccountJSON(t, f.srv.URL+"/token"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := googleplay.New(googleplay.Config{
		PackageName: pkg, ServiceAccount: sa, BaseURL: f.srv.URL, Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const activeJSON = `{
  "kind": "androidpublisher#subscriptionPurchaseV2",
  "startTime": "2026-11-21T10:00:00Z",
  "subscriptionState": "SUBSCRIPTION_STATE_ACTIVE",
  "acknowledgementState": "ACKNOWLEDGEMENT_STATE_PENDING",
  "linkedPurchaseToken": "tok.old",
  "externalAccountIdentifiers": {"obfuscatedExternalAccountId": "acc-1"},
  "lineItems": [{
    "productId": "kergui_max",
    "expiryTime": "2026-11-28T10:00:00.123Z",
    "autoRenewingPlan": {"autoRenewEnabled": true, "recurringPrice": {"currencyCode": "XOF", "units": "2000"}},
    "offerDetails": {"basePlanId": "monthly", "offerId": "trial-7d"},
    "offerPhase": {"freeTrial": {}}
  }]
}`

func TestClient_PurchaseAndAcknowledge(t *testing.T) {
	f := newFakeGoogle(t)
	f.purchase[goodTok] = activeJSON
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	c := newClient(t, f, &now)
	ctx := context.Background()

	p, err := c.Purchase(ctx, goodTok)
	if err != nil {
		t.Fatal(err)
	}
	if p.State != usecase.PlayStateActive || p.Acknowledged || p.LinkedPurchaseToken != "tok.old" || p.ObfuscatedAccountID != "acc-1" {
		t.Fatalf("purchase %+v", p)
	}
	li := p.LineItems[0]
	if len(p.LineItems) != 1 || li.ProductID != "kergui_max" || !li.FreeTrial || li.PriceUnits != 2000 || li.Currency != "XOF" ||
		!li.ExpiresAt.Equal(time.Date(2026, 11, 28, 10, 0, 0, 123e6, time.UTC)) {
		t.Fatalf("line item %+v", li)
	}
	if err := c.Acknowledge(ctx, "kergui_max", goodTok); err != nil {
		t.Fatal(err)
	}
	if len(f.acks) != 1 || f.acks[0] != "kergui_max/tokens/"+goodTok+":acknowledge" {
		t.Fatalf("acks %v", f.acks)
	}
	// The access token is cached for its lifetime, then renewed.
	if f.grants != 1 {
		t.Fatalf("grants = %d, want 1", f.grants)
	}
	now = now.Add(time.Hour)
	if _, err := c.Purchase(ctx, goodTok); err != nil || f.grants != 2 {
		t.Fatalf("after expiry: grants=%d %v", f.grants, err)
	}
}

func TestClient_Errors(t *testing.T) {
	f := newFakeGoogle(t)
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	c := newClient(t, f, &now)
	ctx := context.Background()

	if _, err := c.Purchase(ctx, "tok.unknown"); !errors.Is(err, usecase.ErrPlayPurchaseInvalid) {
		t.Errorf("unknown token: %v", err)
	}
	for _, st := range []int{http.StatusGone, http.StatusBadRequest} {
		f.status = st
		if _, err := c.Purchase(ctx, goodTok); !errors.Is(err, usecase.ErrPlayPurchaseInvalid) {
			t.Errorf("%d: %v", st, err)
		}
	}
	for _, st := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		f.status = st
		if _, err := c.Purchase(ctx, goodTok); !errors.Is(err, usecase.ErrPlayUnavailable) {
			t.Errorf("%d: %v", st, err)
		}
		if err := c.Acknowledge(ctx, "kergui_max", goodTok); !errors.Is(err, usecase.ErrPlayUnavailable) {
			t.Errorf("ack %d: %v", st, err)
		}
	}
	f.status = 0
	if _, err := c.Purchase(ctx, ""); !errors.Is(err, usecase.ErrPlayPurchaseInvalid) {
		t.Errorf("empty token: %v", err)
	}
	f.srv.Close()
	if _, err := c.Purchase(ctx, goodTok); !errors.Is(err, usecase.ErrPlayUnavailable) {
		t.Errorf("Google down: %v", err)
	}
}

func TestParseServiceAccount(t *testing.T) {
	sa, err := googleplay.ParseServiceAccount(serviceAccountJSON(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if sa.ClientEmail != saEmail || sa.TokenURI != googleplay.DefaultTokenURI || sa.KeyID != "kid-1" {
		t.Fatalf("sa %+v", sa)
	}
	for name, raw := range map[string]string{
		"not json":  "nope",
		"no email":  `{"private_key":"x"}`,
		"no key":    `{"client_email":"a@b"}`,
		"bad key":   `{"client_email":"a@b","private_key":"-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"}`,
		"wrong use": `{"type":"authorized_user","client_email":"a@b","private_key":"x"}`,
	} {
		if _, err := googleplay.ParseServiceAccount([]byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
