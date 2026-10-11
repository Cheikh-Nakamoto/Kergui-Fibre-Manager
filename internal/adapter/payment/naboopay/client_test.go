package naboopay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

func TestCreateCheckout_SendsV2Shape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/transactions" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"order_id":"ord-1","checkout_url":"https://checkout.naboopay.com/x","amount":1250,"transaction_status":"pending"}`))
	}))
	defer srv.Close()

	res, err := New("k", srv.URL).CreateCheckout(context.Background(), port.CheckoutRequest{
		Label: "Internet", Description: "2,5 Go", AmountXOF: 1250, CustomerName: "Awa Diop Sarr", Phone: "+221771234567",
		SuccessURL: "https://pay.example/ok", ErrorURL: "https://pay.example/ko",
	})
	if err != nil || res.OrderID != "ord-1" || res.AmountXOF != 1250 {
		t.Fatalf("res = %+v, %v", res, err)
	}
	methods := got["method_of_payment"].([]any)
	if len(methods) != 2 || methods[0] != "wave" || methods[1] != "orange_money" {
		t.Errorf("method_of_payment = %v, want lowercase wave/orange_money", methods)
	}
	prod := got["products"].([]any)[0].(map[string]any)
	if len(prod) != 4 || prod["price"].(float64) != 1250 || prod["quantity"].(float64) != 1 {
		t.Errorf("product = %v, want exactly name/price/quantity/description", prod)
	}
	cust := got["customer"].(map[string]any)
	if cust["first_name"] != "Awa" || cust["last_name"] != "Diop Sarr" || cust["phone"] != "+221771234567" {
		t.Errorf("customer = %v", cust)
	}
}

func TestTransactionStatus_AndErrors(t *testing.T) {
	status := "paid"
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"order_id":"ord-1","transaction_status":"` + status + `","error":"bad key"}`))
	}))
	defer srv.Close()
	c := New("k", srv.URL)
	ctx := context.Background()

	if s, err := c.TransactionStatus(ctx, "ord-1"); err != nil || s != domain.PaymentPaid {
		t.Errorf("paid -> %v, %v", s, err)
	}
	status = "something_new"
	if s, _ := c.TransactionStatus(ctx, "ord-1"); s != domain.PaymentPending {
		t.Errorf("unknown status must stay pending, got %v", s)
	}
	code = http.StatusUnauthorized
	if _, err := c.TransactionStatus(ctx, "ord-1"); !errors.Is(err, domain.ErrPaymentUnavailable) {
		t.Errorf("401 -> %v, want ErrPaymentUnavailable", err)
	}
}

func TestMapStatus(t *testing.T) {
	for s, want := range map[string]domain.PaymentStatus{
		"paid": domain.PaymentPaid, "paid_and_blocked": domain.PaymentPaid, "completed": domain.PaymentPaid,
		"pending": domain.PaymentPending, "cancelled": domain.PaymentCancelled, "refunded": domain.PaymentRefunded,
		"": domain.PaymentPending,
	} {
		if got := MapStatus(s); got != want {
			t.Errorf("MapStatus(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestCreateCheckout_WithoutPhoneOmitsCustomer(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"order_id":"o","checkout_url":"https://c/o"}`))
	}))
	defer srv.Close()
	if _, err := New("k", srv.URL).CreateCheckout(context.Background(), port.CheckoutRequest{Label: "x", AmountXOF: 100}); err != nil {
		t.Fatal(err)
	}
	if _, present := got["customer"]; present {
		t.Errorf("customer sent without a phone: %v", got["customer"])
	}
}
