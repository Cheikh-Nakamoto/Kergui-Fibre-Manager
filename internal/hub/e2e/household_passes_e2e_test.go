package e2e

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_HouseholdPasses is issue #98: from a household's page an admin
// renews a box's pass by hand (tier, days, the money received, counted in
// the revenue), gives a lifetime pass and takes it back. Each gesture is in
// the action log with its reason, and the household's phone gets a push the
// app files in its notifications. The lifetime pass issues the usual
// 30-day token, is in no expiring list and no renewal ledger, and says
// « lifetime » to the app. A household owner is refused all of it.
func TestE2E_HouseholdPasses(t *testing.T) {
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	number := "+221" + local
	w := newWorldWith(t, fastTimings, withPhoneOwner(number))
	box1, box2 := "HP"+local+"A", "HP"+local+"B"

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
	token := "ExponentPushToken[hp-" + local + "]"
	if code, body := w.owner.call(http.MethodPut, w.owner.tenantPath("push-tokens"),
		map[string]any{"token": token, "platform": "android"}); code != http.StatusNoContent {
		t.Fatalf("PUT push-tokens = %d %v", code, body)
	}

	base := "/api/v1/admin/households/" + w.owner.tenantID + "/passes/"
	routes := []struct {
		path string
		body any
	}{
		{base + "renew", map[string]any{"boxId": box1, "tier": "max", "reason": "moi-même"}},
		{base + "lifetime", map[string]any{"boxId": box1, "tier": "max", "reason": "moi-même"}},
		{base + "lifetime/revoke", map[string]any{"boxId": box1, "reason": "moi-même"}},
	}
	anon := &ownerApp{t: t, hub: w.hub}
	for _, r := range routes {
		if code, _ := anon.call(http.MethodPost, r.path, r.body); code != http.StatusUnauthorized {
			t.Fatalf("POST %s anonymously = %d, want 401", r.path, code)
		}
		if code, _ := w.owner.call(http.MethodPost, r.path, r.body); code != http.StatusForbidden {
			t.Fatalf("POST %s as the owner = %d, want 403", r.path, code)
		}
	}

	email, password := "hpasses-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	pushed := func(title string) map[string]string {
		t.Helper()
		for _, p := range w.pusher.Pushed() {
			if p.Target.Token == token && p.Msg.Data["title"] == title {
				return p.Msg.Data
			}
		}
		t.Fatalf("no push %q to the household's phone", title)
		return nil
	}
	passOf := func(boxID string) map[string]any {
		t.Helper()
		code, body := ops.call(http.MethodGet, "/api/v1/admin/households/"+w.owner.tenantID, nil)
		if code != http.StatusOK || str(body, "accountId") != accountID {
			t.Fatalf("household = %d %v", code, body)
		}
		passes, _ := body["passes"].([]any)
		for _, p := range passes {
			if m := p.(map[string]any); str(m, "boxId") == boxID {
				return m
			}
		}
		return nil
	}

	// 1. Renew: a reason is required, days default to 30; a first renewal
	// starts now, the next adds to the end.
	if code, _ = ops.call(http.MethodPost, base+"renew", map[string]any{"boxId": box1, "tier": "max", "reason": ""}); code != http.StatusBadRequest {
		t.Fatalf("renew without a reason = %d, want 400", code)
	}
	if code, _ = ops.call(http.MethodPost, base+"renew", map[string]any{"boxId": box1, "tier": "free", "reason": "Wave reçu"}); code != http.StatusBadRequest {
		t.Fatalf("renew on Free = %d, want 400", code)
	}
	if code, _ = ops.call(http.MethodPost, base+"renew", map[string]any{"boxId": box1, "tier": "max", "days": 400, "reason": "Wave reçu"}); code != http.StatusBadRequest {
		t.Fatalf("renew 400 days = %d, want 400", code)
	}
	start := time.Now()
	code, body = ops.call(http.MethodPost, base+"renew", map[string]any{"boxId": box1, "tier": "max", "reason": "paiement Wave reçu à la main", "amountXof": 2000})
	if code != http.StatusOK {
		t.Fatalf("renew = %d %v", code, body)
	}
	first, _ := time.Parse(time.RFC3339, str(body, "expiresAt"))
	if first.Before(start.Add(30*24*time.Hour-time.Minute)) || first.After(time.Now().Add(30*24*time.Hour+time.Minute)) {
		t.Fatalf("renewed until %v, want 30 days from now", first)
	}
	if n, _ := body["notified"].(map[string]any)["sent"].(float64); n != 1 {
		t.Fatalf("notified = %v, want the household's phone", body["notified"])
	}
	if d := pushed("Pass prolongé"); d["kind"] != "announcement" || d["tenantId"] != w.owner.tenantID || !strings.Contains(d["body"], box1) {
		t.Fatalf("renewal push = %v", d)
	}
	code, body = ops.call(http.MethodPost, base+"renew", map[string]any{"boxId": box1, "tier": "max", "days": 10, "reason": "geste"})
	second, _ := time.Parse(time.RFC3339, str(body, "expiresAt"))
	// first came from Go, second from Postgres: microseconds apart at most.
	if code != http.StatusOK || second.Sub(first.Add(10*24*time.Hour)).Abs() > time.Millisecond {
		t.Fatalf("second renewal = %d %v, want from the end %v", code, body, first)
	}
	// The money received by hand is revenue; the gift is not.
	code, body = ops.call(http.MethodGet, "/api/v1/admin/payments/revenue", nil)
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(body["byPass"]), "max_30d") {
		t.Fatalf("revenue = %d %v, want the manual max_30d", code, body)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_licensing_payments WHERE account_id = $1 AND provider = 'manual' AND amount_xof = 2000 AND pass_code = 'max_30d'`, accountID); n != 1 {
		t.Fatalf("%d manual payments, want 1", n)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_licensing_payments WHERE account_id = $1 AND provider = 'support' AND amount_xof = 0 AND pass_code = 'max_10d'`, accountID); n != 1 {
		t.Fatalf("%d gifts, want 1", n)
	}
	// The renewal ledger saw the second one (the first started a pass).
	if n := countRows(t, `SELECT count(*) FROM hub_licensing_renewals WHERE account_id = $1 AND box_id = $2`, accountID, box1); n != 1 {
		t.Fatalf("%d renewals recorded, want 1", n)
	}

	// 2. Lifetime on box 1 (Max running) and on box 2 (nothing).
	code, body = ops.call(http.MethodPost, base+"lifetime", map[string]any{"boxId": box1, "tier": "max", "reason": "testeur bêta"})
	if code != http.StatusOK || body["lifetime"] != true {
		t.Fatalf("lifetime = %d %v", code, body)
	}
	if d := pushed("Pass à vie"); d["kind"] != "announcement" {
		t.Fatalf("lifetime push = %v", d)
	}
	if code, body = ops.call(http.MethodPost, base+"lifetime", map[string]any{"boxId": box2, "tier": "basic", "reason": "ami de la famille"}); code != http.StatusOK {
		t.Fatalf("lifetime on box 2 = %d %v", code, body)
	}
	if p := passOf(box1); p == nil || p["lifetime"] != true || p["active"] != true || str(p, "tier") != "max" {
		t.Fatalf("box 1 = %v", p)
	}
	// No renewal recorded for it, nothing to renew or extend.
	if n := countRows(t, `SELECT count(*) FROM hub_licensing_renewals WHERE account_id = $1 AND box_id = $2`, accountID, box1); n != 1 {
		t.Fatalf("%d renewals after the lifetime grant, want still 1", n)
	}
	if code, _ = ops.call(http.MethodPost, base+"renew", map[string]any{"boxId": box1, "tier": "max", "reason": "Wave reçu"}); code != http.StatusConflict {
		t.Fatalf("renew a lifetime pass = %d, want 409", code)
	}
	if code, _ = ops.call(http.MethodPost, "/api/v1/admin/support/accounts/"+accountID+"/extend", map[string]any{"boxId": box1, "days": 5, "reason": "panne"}); code != http.StatusConflict {
		t.Fatalf("extend a lifetime pass = %d, want 409", code)
	}
	// Never in the expiring list, even with its far date.
	code, body = ops.call(http.MethodGet, "/api/v1/admin/passes/expiring?q="+local, nil)
	if code != http.StatusOK || strings.Contains(fmt.Sprint(body["passes"]), box1) {
		t.Fatalf("expiring = %d %v", code, body)
	}
	code, body = ops.call(http.MethodGet, "/api/v1/admin/passes/renewals", nil)
	if active, _ := body["active"].(map[string]any); code != http.StatusOK || active["lifetime"].(float64) < 2 {
		t.Fatalf("renewals active = %d %v", code, body["active"])
	}
	// The app: the usual 30-day token, and « lifetime » to show « à vie ».
	app := &ownerApp{t: t, hub: w.hub, token: w.owner.session}
	code, body = app.call(http.MethodPost, "/licensing/v1/token", map[string]any{"boxId": box1})
	exp, _ := time.Parse(time.RFC3339, str(body, "expiresAt"))
	if code != http.StatusOK || str(body, "token") == "" || body["lifetime"] != true || str(body, "tier") != "max" ||
		exp.After(time.Now().Add(30*24*time.Hour+time.Minute)) {
		t.Fatalf("token = %d %v", code, body)
	}
	code, body = app.call(http.MethodGet, "/licensing/v1/subscriptions", nil)
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(body["subscriptions"]), "lifetime:true") {
		t.Fatalf("subscriptions = %d %v", code, body)
	}

	// 3. Revoke: box 1 gets back the Max it had paid for; box 2 is Free.
	code, body = ops.call(http.MethodPost, base+"lifetime/revoke", map[string]any{"boxId": box1, "reason": "fin du test"})
	back, _ := time.Parse(time.RFC3339, str(body, "expiresAt"))
	if code != http.StatusOK || body["active"] != true || !back.Equal(second) {
		t.Fatalf("revoke box 1 = %d %v, want back to %v", code, body, second)
	}
	if d := pushed("Pass à vie retiré"); !strings.Contains(d["body"], "continue jusqu’au") {
		t.Fatalf("revoke push = %v", d)
	}
	code, body = ops.call(http.MethodPost, base+"lifetime/revoke", map[string]any{"boxId": box2, "reason": "fin du test"})
	if code != http.StatusOK || body["active"] != false || str(body, "tier") != "free" {
		t.Fatalf("revoke box 2 = %d %v", code, body)
	}
	if code, _ = ops.call(http.MethodPost, base+"lifetime/revoke", map[string]any{"boxId": box2, "reason": "encore"}); code != http.StatusConflict {
		t.Fatalf("revoke twice = %d, want 409", code)
	}
	if p := passOf(box1); p == nil || p["lifetime"] == true {
		t.Fatalf("box 1 after revoke = %v", p)
	}

	// 4. The action log: who, when, what, why.
	for kind, reason := range map[string]string{
		"PASS_RENEWED_BY_ADMIN": "paiement Wave reçu à la main",
		"PASS_LIFETIME_GRANTED": "testeur bêta",
		"PASS_LIFETIME_REVOKED": "fin du test",
	} {
		waitForRow(t, 10*time.Second,
			`SELECT count(*) FROM audit_logs WHERE action = $1 AND tenant_id = $2 AND target_id = $3 AND metadata->>'reason' = $4 AND actor_id IS NOT NULL`,
			kind, w.owner.tenantID, accountID, reason)
	}
	code, body = ops.call(http.MethodGet, "/api/v1/admin/households/"+w.owner.tenantID+"/history", nil)
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(body["events"]), "à vie accordé") {
		t.Fatalf("history = %d %v", code, body)
	}
	// The pushes are in « Messages »' history too.
	if n := countRows(t, `SELECT count(*) FROM hub_messages WHERE tenant_id = $1 AND audience = 'household'`, w.owner.tenantID); n < 4 {
		t.Fatalf("%d messages recorded for the household, want the 4 pushed (2 renewals, grant, revoke)", n)
	}
}
