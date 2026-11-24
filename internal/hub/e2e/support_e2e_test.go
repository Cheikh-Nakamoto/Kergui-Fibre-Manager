package e2e

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_Support is issue #78: an admin finds a customer by their number
// typed any way, sees their account, household and passes, extends a pass
// (the end date moves, the log keeps the reason), moves it past the
// customer's own transfer limit, and revokes their sessions (the customer's
// next call is refused). A household owner is refused all of it, and no
// sign-in code ever leaves from the back-office.
func TestE2E_Support(t *testing.T) {
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	number := "+221" + local
	w := newWorldWith(t, fastTimings, withPhoneOwner(number))
	box1, box2 := "SUP"+local+"A", "SUP"+local+"B"

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var accountID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hub_licensing_accounts WHERE phone = $1`, number).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	acct := "/api/v1/admin/support/accounts/" + accountID

	// Nobody but a platform admin: anonymous 401, the owner 403.
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/v1/admin/support/lookup?phone=" + local, nil},
		{http.MethodGet, acct, nil},
		{http.MethodGet, "/api/v1/admin/households/" + w.owner.tenantID + "/history", nil},
		{http.MethodPost, acct + "/extend", map[string]any{"boxId": box1, "days": 30, "reason": "moi-même"}},
		{http.MethodPost, acct + "/transfer", map[string]any{"fromBoxId": box1, "toBoxId": box2, "reason": "moi-même"}},
		{http.MethodPost, acct + "/revoke-sessions", map[string]any{"reason": "moi-même"}},
	}
	anon := &ownerApp{t: t, hub: w.hub}
	for _, r := range routes {
		if code, _ := anon.call(r.method, r.path, r.body); code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymously = %d, want 401", r.method, r.path, code)
		}
		if code, _ := w.owner.call(r.method, r.path, r.body); code != http.StatusForbidden {
			t.Fatalf("%s %s as the owner = %d, want 403", r.method, r.path, code)
		}
	}

	email, password := "support-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	// 1. Lookup, by the number in any form.
	for _, raw := range []string{local, local[:2] + " " + local[2:5] + " " + local[5:], number, "00221" + local, "+221 " + local} {
		code, body = ops.call(http.MethodGet, "/api/v1/admin/support/lookup?phone="+url.QueryEscape(raw), nil)
		if code != http.StatusOK || str(body, "accountId") != accountID || str(body, "phone") != number {
			t.Fatalf("lookup %q = %d %v", raw, code, body)
		}
	}
	if code, _ = ops.call(http.MethodGet, "/api/v1/admin/support/lookup?phone=pas-un-numero", nil); code != http.StatusBadRequest {
		t.Fatalf("lookup of a bad number = %d, want 400", code)
	}
	unknown := fmt.Sprintf("78%07d", mathrand.IntN(10_000_000))
	if code, _ = ops.call(http.MethodGet, "/api/v1/admin/support/lookup?phone="+unknown, nil); code != http.StatusNotFound {
		t.Fatalf("lookup of an unknown number = %d, want 404", code)
	}

	// A pass on the first box, granted as support already could.
	if code, body = ops.call(http.MethodPost, "/api/v1/admin/passes/grant", map[string]string{
		"accountId": accountID, "boxId": box1, "passCode": "max_1m", "reason": "mise en place e2e",
	}); code != http.StatusOK {
		t.Fatalf("grant = %d %v", code, body)
	}

	// 2. The customer's page.
	view := func() map[string]any {
		t.Helper()
		code, body := ops.call(http.MethodGet, acct, nil)
		if code != http.StatusOK {
			t.Fatalf("customer = %d %v", code, body)
		}
		return body
	}
	sub := func(v map[string]any, boxID string) map[string]any {
		subs, _ := v["subscriptions"].([]any)
		for _, s := range subs {
			if m := s.(map[string]any); str(m, "boxId") == boxID {
				return m
			}
		}
		return nil
	}
	v := view()
	account, _ := v["account"].(map[string]any)
	household, _ := v["household"].(map[string]any)
	members, _ := v["members"].([]any)
	payments, _ := v["payments"].([]any)
	sessions, _ := v["sessions"].(map[string]any)
	if str(account, "phone") != number || str(household, "tenantId") != w.owner.tenantID || len(members) != 1 || len(payments) != 1 {
		t.Fatalf("customer view = %v", v)
	}
	if n, _ := sessions["active"].(float64); n < 1 {
		t.Fatalf("active sessions = %v, want the owner's phone", sessions)
	}
	if tr, _ := v["trial"].(map[string]any); str(tr, "state") == "" {
		t.Fatalf("trial = %v", v["trial"])
	}
	if _, ok := v["waitingPasses"].([]any); !ok {
		t.Fatalf("waitingPasses = %v", v["waitingPasses"])
	}
	s1 := sub(v, box1)
	if s1 == nil || s1["active"] != true {
		t.Fatalf("box %s = %v", box1, v["subscriptions"])
	}
	before, _ := time.Parse(time.RFC3339, str(s1, "expiresAt"))

	// 3. Extend: days and a reason are required; the end date moves.
	for _, bad := range []map[string]any{
		{"boxId": box1, "days": 10},
		{"boxId": box1, "days": 0, "reason": "panne fibre"},
		{"boxId": box1, "days": 366, "reason": "panne fibre"},
	} {
		if code, _ = ops.call(http.MethodPost, acct+"/extend", bad); code != http.StatusBadRequest {
			t.Fatalf("extend %v = %d, want 400", bad, code)
		}
	}
	const extendReason = "panne fibre du quartier, 10 jours"
	if code, body = ops.call(http.MethodPost, acct+"/extend", map[string]any{"boxId": box1, "days": 10, "reason": extendReason}); code != http.StatusOK {
		t.Fatalf("extend = %d %v", code, body)
	}
	after, _ := time.Parse(time.RFC3339, str(sub(view(), box1), "expiresAt"))
	if !after.Equal(before.AddDate(0, 0, 10)) {
		t.Fatalf("end date %v after extending %v by 10 days", after, before)
	}
	entry := waitLogEntry(t, ops, email, "PASS_EXTENDED")
	if str(entry, "reason") != extendReason || str(entry.target(), "id") != accountID || str(entry, "tenantId") != w.owner.tenantID {
		t.Fatalf("extend log entry = %v", entry)
	}

	// 4. Transfer past the customer's limit: they already moved it twice.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO hub_licensing_transfers (account_id, from_box_id, to_box_id, at)
			VALUES ($1, 'OLD1', 'OLD2', now())`, accountID); err != nil {
			t.Fatal(err)
		}
	}
	if code, body = ops.call(http.MethodPost, acct+"/transfer", map[string]any{"fromBoxId": box1, "toBoxId": box2}); code != http.StatusBadRequest {
		t.Fatalf("transfer without a reason = %d, want 400", code)
	}
	if code, body = ops.call(http.MethodPost, acct+"/transfer", map[string]any{"fromBoxId": box1, "toBoxId": box2, "reason": "box remplacée par Orange"}); code != http.StatusOK {
		t.Fatalf("transfer = %d %v", code, body)
	}
	v = view()
	if s2 := sub(v, box2); s2 == nil || s2["active"] != true || !sameTime(str(s2, "expiresAt"), after) {
		t.Fatalf("box %s after the transfer = %v", box2, v["subscriptions"])
	}
	if sub(v, box1) != nil {
		t.Fatalf("the pass stayed on %s: %v", box1, v["subscriptions"])
	}
	if entry = waitLogEntry(t, ops, email, "PASS_TRANSFERRED_BY_ADMIN"); str(entry, "reason") != "box remplacée par Orange" {
		t.Fatalf("transfer log entry = %v", entry)
	}

	// 5. The household's history: the payment and the admin gestures,
	// newest first, a page at a time.
	hist := "/api/v1/admin/households/" + w.owner.tenantID + "/history"
	code, body = ops.call(http.MethodGet, hist, nil)
	events, _ := body["events"].([]any)
	kinds := map[string]bool{}
	var last time.Time
	for i, e := range events {
		m := e.(map[string]any)
		kinds[str(m, "kind")] = true
		at, _ := time.Parse(time.RFC3339Nano, str(m, "at"))
		if i > 0 && at.After(last) {
			t.Fatalf("history not newest first: %v", events)
		}
		last = at
	}
	if code != http.StatusOK || !kinds["payment"] || !kinds["admin"] {
		t.Fatalf("history = %d %v", code, body)
	}
	code, body = ops.call(http.MethodGet, hist+"?limit=1", nil)
	page, _ := body["events"].([]any)
	if code != http.StatusOK || len(page) != 1 || str(body, "next") == "" {
		t.Fatalf("history page 1 = %d %v", code, body)
	}
	code, body = ops.call(http.MethodGet, hist+"?limit=1&before="+str(body, "next"), nil)
	if page2, _ := body["events"].([]any); code != http.StatusOK || len(page2) != 1 ||
		str(page2[0].(map[string]any), "id") == str(page[0].(map[string]any), "id") {
		t.Fatalf("history page 2 = %d %v", code, body)
	}

	// 6. Revoke: the customer's session works, then no longer does.
	customer := &ownerApp{t: t, hub: w.hub, token: w.owner.session}
	if code, body = customer.call(http.MethodGet, "/licensing/v1/subscriptions", nil); code != http.StatusOK {
		t.Fatalf("the customer's session before = %d %v", code, body)
	}
	if code, _ = ops.call(http.MethodPost, acct+"/revoke-sessions", map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("revoke without a reason = %d, want 400", code)
	}
	if code, body = ops.call(http.MethodPost, acct+"/revoke-sessions", map[string]any{"reason": "téléphone volé"}); code != http.StatusOK {
		t.Fatalf("revoke = %d %v", code, body)
	} else if n, _ := body["sessionsRevoked"].(float64); n < 1 {
		t.Fatalf("revoke = %v, want at least the owner's session", body)
	}
	if code, _ = customer.call(http.MethodGet, "/licensing/v1/subscriptions", nil); code != http.StatusUnauthorized {
		t.Fatalf("the customer's next call = %d, want 401", code)
	}
	if code, _ = customer.call(http.MethodPost, "/licensing/v1/session/hub", nil); code != http.StatusUnauthorized {
		t.Fatalf("a new hub session from the revoked one = %d, want 401", code)
	}
	if code, _ = anon.call(http.MethodPost, "/api/v1/auth/refresh", map[string]any{"refreshToken": w.owner.refreshToken}); code != http.StatusUnauthorized {
		t.Fatalf("the customer's kernel refresh = %d, want 401", code)
	}
	if entry = waitLogEntry(t, ops, email, "SESSIONS_REVOKED"); str(entry, "reason") != "téléphone volé" {
		t.Fatalf("revoke log entry = %v", entry)
	}

	// No sign-in code ever left from the back-office: only the owner's own.
	codes := 0
	for _, s := range w.notifier.Sent() {
		if string(s.To) == number && strings.Contains(s.Text, "code de connexion") {
			codes++
		}
	}
	if codes != 1 {
		t.Fatalf("%d sign-in codes sent to the customer, want their own one", codes)
	}
}

type logRow map[string]any

func (e logRow) target() map[string]any { m, _ := e["target"].(map[string]any); return m }

// waitLogEntry waits for the admin's newest action log entry of a kind: the
// audit rows come through the outbox, a moment later.
func waitLogEntry(t *testing.T, ops *ownerApp, email, kind string) logRow {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, body := ops.call(http.MethodGet, "/api/v1/admin/audit?admin="+url.QueryEscape(email)+"&kind="+kind, nil)
		if entries, _ := body["entries"].([]any); code == http.StatusOK && len(entries) > 0 {
			return logRow(entries[0].(map[string]any))
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s entry in the log: %d %v", kind, code, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func sameTime(s string, want time.Time) bool {
	got, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && got.Equal(want)
}
