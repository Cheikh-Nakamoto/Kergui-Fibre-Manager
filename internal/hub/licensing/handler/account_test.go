package handler_test

import (
	"net/http"
	"testing"
)

// TestCustomerArea is issue #63 on the hub's side: a signed-in number reads
// its trial and asks for it, and reads its payments; nobody else's.
func TestCustomerArea(t *testing.T) {
	e := newEnv(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/licensing/v1/payments"}, {"GET", "/licensing/v1/trial"}, {"POST", "/licensing/v1/trial"},
	} {
		if st, _, _ := e.do(c.method, c.path, "", nil); st != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session = %d, want 401", c.method, c.path, st)
		}
	}
	tok := e.signIn()

	st, body, _ := e.do("GET", "/licensing/v1/trial", tok, nil)
	if st != 200 || body["state"] != "available" || body["days"] != float64(7) {
		t.Fatalf("trial before = %d %v", st, body)
	}
	st, body, _ = e.do("POST", "/licensing/v1/trial", tok, nil)
	if st != 200 || body["state"] != "pending" || body["requestedAt"] == nil {
		t.Fatalf("ask for the trial = %d %v", st, body)
	}
	if st, _, _ = e.do("POST", "/licensing/v1/trial", tok, nil); st != http.StatusConflict {
		t.Fatalf("a second trial = %d, want 409", st)
	}
	// Binding the box starts the 7 days.
	if st, body, _ = e.do("POST", "/licensing/v1/bind", tok, map[string]any{"boxId": box}); st != 200 {
		t.Fatalf("bind = %d %v", st, body)
	}
	st, body, _ = e.do("GET", "/licensing/v1/trial", tok, nil)
	if st != 200 || body["state"] != "running" || body["boxId"] != box || body["endsAt"] == nil {
		t.Fatalf("trial after bind = %d %v", st, body)
	}

	// Payments: none, then the pending checkout.
	st, body, _ = e.do("GET", "/licensing/v1/payments", tok, nil)
	if list, _ := body["payments"].([]any); st != 200 || len(list) != 0 {
		t.Fatalf("payments before = %d %v", st, body)
	}
	if st, body, _ = e.do("POST", "/licensing/v1/checkout", tok, map[string]any{"boxId": box, "pass": "max_1m"}); st != http.StatusCreated {
		t.Fatalf("checkout = %d %v", st, body)
	}
	st, body, _ = e.do("GET", "/licensing/v1/payments", tok, nil)
	list, _ := body["payments"].([]any)
	if st != 200 || len(list) != 1 {
		t.Fatalf("payments after = %d %v", st, body)
	}
	p := list[0].(map[string]any)
	if p["pass"] != "max_1m" || p["status"] != "pending" || p["amountXof"] != float64(2000) || p["boxId"] != box {
		t.Fatalf("payment = %v", p)
	}

	// Another number sees none of it.
	other := e.signInAs("+221770000001")
	if _, body, _ = e.do("GET", "/licensing/v1/payments", other, nil); len(body["payments"].([]any)) != 0 {
		t.Fatalf("another number sees %v", body["payments"])
	}
}
