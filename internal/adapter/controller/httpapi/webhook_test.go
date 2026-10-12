package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const testSecret = "whsec_test"

// statusGateway answers TransactionStatus with whatever NabooPay "says".
type statusGateway struct{ status atomic.Value }

func (g *statusGateway) CreateCheckout(context.Context, port.CheckoutRequest) (port.CheckoutResult, error) {
	return port.CheckoutResult{}, nil
}
func (g *statusGateway) TransactionStatus(context.Context, string) (domain.PaymentStatus, error) {
	return g.status.Load().(domain.PaymentStatus), nil
}
func (g *statusGateway) CancelCheckout(context.Context, string) error { return nil }

func newWebhookAPI(t *testing.T, secret string, gw *statusGateway) (*httptest.Server, *sqlite.PaymentRepo) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	payments := sqlite.NewPaymentRepo(db)
	now := time.Now()
	if err := payments.Create(context.Background(), domain.Payment{
		ID: "pay-1", RouterID: "r", MAC: domain.MustMAC("aa:bb:cc:00:00:01"), Period: "2026-10",
		AmountXOF: 500, OrderID: "ord-42", Status: domain.PaymentPending, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	logger := logging.New(io.Discard, false)
	billing := usecase.NewBilling(sqlite.NewHistoryRepo(db), sqlite.NewUsageRepo(db), payments, sqlite.NewSettingsRepo(db),
		sqlite.NewMemberRepo(db), gw, clock.Real{}, logger, "https://pay.example")
	api := httpapi.New(httpapi.Services{Billing: billing, Logger: logger}, httpapi.Config{Version: "test", WebhookSecret: secret})
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return srv, payments
}

func sign(body string) string {
	m := hmac.New(sha256.New, []byte(testSecret))
	m.Write([]byte(body))
	return hex.EncodeToString(m.Sum(nil))
}

func postWebhook(t *testing.T, url, body, sig string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/paiement/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if sig != "" {
		req.Header.Set("X-Signature", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitStatus(t *testing.T, repo *sqlite.PaymentRepo, want domain.PaymentStatus) domain.PaymentStatus {
	t.Helper()
	var got domain.PaymentStatus
	for range 50 {
		p, _, err := repo.Get(context.Background(), "pay-1")
		if err != nil {
			t.Fatal(err)
		}
		if got = p.Status; got == want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

const paidBody = `{"order_id":"ord-42","transaction_status":"paid","amount":500}`

func TestWebhook_SignedCallChecksNabooPay(t *testing.T) {
	gw := &statusGateway{}
	gw.status.Store(domain.PaymentPaid)
	srv, repo := newWebhookAPI(t, testSecret, gw)
	if code := postWebhook(t, srv.URL, paidBody, sign(paidBody)); code != http.StatusOK {
		t.Fatalf("signed webhook -> %d, want 200", code)
	}
	if got := waitStatus(t, repo, domain.PaymentPaid); got != domain.PaymentPaid {
		t.Errorf("payment is %s after a signed webhook, want paid", got)
	}
}

func TestWebhook_NeverTrustsThePayload(t *testing.T) {
	gw := &statusGateway{}
	gw.status.Store(domain.PaymentPending) // NabooPay's API says: not paid
	srv, repo := newWebhookAPI(t, testSecret, gw)
	if code := postWebhook(t, srv.URL, paidBody, sign(paidBody)); code != http.StatusOK {
		t.Fatalf("signed webhook -> %d, want 200", code)
	}
	time.Sleep(100 * time.Millisecond)
	if p, _, _ := repo.Get(context.Background(), "pay-1"); p.Status != domain.PaymentPending {
		t.Errorf("payment is %s: a webhook saying \"paid\" must not override NabooPay's API", p.Status)
	}
}

func TestWebhook_RejectsBadSignature(t *testing.T) {
	gw := &statusGateway{}
	gw.status.Store(domain.PaymentPaid)
	srv, repo := newWebhookAPI(t, testSecret, gw)
	for name, sig := range map[string]string{"missing": "", "wrong": sign(paidBody + " "), "not hex": "zz"} {
		if code := postWebhook(t, srv.URL, paidBody, sig); code != http.StatusUnauthorized {
			t.Errorf("%s signature -> %d, want 401", name, code)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if p, _, _ := repo.Get(context.Background(), "pay-1"); p.Status != domain.PaymentPending {
		t.Errorf("payment is %s after unsigned calls, want pending", p.Status)
	}
}

func TestWebhook_OffWithoutSecret(t *testing.T) {
	gw := &statusGateway{}
	gw.status.Store(domain.PaymentPaid)
	srv, _ := newWebhookAPI(t, "", gw)
	if code := postWebhook(t, srv.URL, paidBody, sign(paidBody)); code != http.StatusNotFound {
		t.Errorf("webhook without a secret -> %d, want 404", code)
	}
}

func TestWebhook_MissingOrderID(t *testing.T) {
	gw := &statusGateway{}
	gw.status.Store(domain.PaymentPaid)
	srv, _ := newWebhookAPI(t, testSecret, gw)
	body := `{"transaction_status":"paid"}`
	if code := postWebhook(t, srv.URL, body, sign(body)); code != http.StatusBadRequest {
		t.Errorf("no order_id -> %d, want 400", code)
	}
}
