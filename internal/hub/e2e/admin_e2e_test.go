package e2e

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_BackOffice is issue #62 on the hub's side: a household owner is
// refused the back-office API; a platform administrator created with
// `kergui-hub admin create` signs in, sees the hub's health, finds the
// household by its phone number, and grants it a pass, recorded at 0 F and
// audited.
func TestE2E_BackOffice(t *testing.T) {
	// A fresh number each run: the sign-in codes are rate-limited per number
	// in the shared test database.
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	number := "+221" + local
	w := newWorldWith(t, fastTimings, withPhoneOwner(number))

	// An owner, even of their own household, is not an administrator.
	for _, p := range []string{"/api/v1/admin/health", "/api/v1/admin/households", "/api/v1/admin/passes"} {
		if code, _ := w.owner.call(http.MethodGet, p, nil); code != http.StatusForbidden {
			t.Fatalf("GET %s as an owner = %d, want 403", p, code)
		}
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// A fresh address each run, never deleted: the audit rows of earlier
	// runs keep pointing at their admin.
	email, password := "ops-"+local+"@kergui.test", "a-long-admin-password"
	if created, err := admin.CreateAdmin(ctx, pool, email, password); err != nil || !created {
		t.Fatalf("CreateAdmin = %v, %v", created, err)
	}
	if _, err := admin.CreateAdmin(ctx, pool, email, "short"); err == nil {
		t.Fatal("a short password was accepted")
	}

	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	if code, body = ops.call(http.MethodGet, "/api/v1/admin/me", nil); code != http.StatusOK || str(body, "email") != email {
		t.Fatalf("me = %d %v", code, body)
	}
	if code, body = ops.call(http.MethodGet, "/api/v1/admin/health", nil); code != http.StatusOK || str(body, "database") != "ok" {
		t.Fatalf("health = %d %v", code, body)
	}

	// Found by the digits of its number, typed any way.
	code, body = ops.call(http.MethodGet, "/api/v1/admin/households?q="+local[:2]+"+"+local[2:], nil)
	list, _ := body["households"].([]any)
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("households?q = %d, %d results: %v", code, len(list), body)
	}
	if code, body = ops.call(http.MethodGet, "/api/v1/admin/households/"+w.owner.tenantID, nil); code != http.StatusOK {
		t.Fatalf("household = %d %v", code, body)
	} else if members, _ := body["members"].([]any); len(members) != 1 {
		t.Fatalf("household members = %v, want the owner", body["members"])
	}

	// A trial asked for on the web waits for a box: no box, no start yet.
	if _, err := pool.Exec(ctx, `INSERT INTO hub_licensing_trials (phone, account_id, requested_at)
		SELECT phone, id, now() FROM hub_licensing_accounts WHERE phone = $1`, number); err != nil {
		t.Fatal(err)
	}
	code, body = ops.call(http.MethodGet, "/api/v1/admin/passes?q="+local, nil)
	accounts, _ := body["accounts"].([]any)
	if code != http.StatusOK || len(accounts) != 1 {
		t.Fatalf("passes?q = %d %v", code, body)
	}
	accountID := str(accounts[0].(map[string]any), "accountId")
	if tr, _ := accounts[0].(map[string]any)["trial"].(map[string]any); tr == nil || tr["startedAt"] != nil {
		t.Fatalf("trial = %v, want a pending one", accounts[0].(map[string]any)["trial"])
	}

	// A grant needs a reason.
	if code, _ = ops.call(http.MethodPost, "/api/v1/admin/passes/grant", map[string]string{
		"accountId": accountID, "passCode": "max_1m",
	}); code != http.StatusBadRequest {
		t.Fatalf("grant without a reason = %d, want 400", code)
	}
	if code, body = ops.call(http.MethodPost, "/api/v1/admin/passes/grant", map[string]string{
		"accountId": accountID, "passCode": "max_1m", "reason": "geste commercial e2e",
	}); code != http.StatusOK {
		t.Fatalf("grant = %d %v", code, body)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_licensing_payments WHERE account_id = $1 AND provider = 'support' AND amount_xof = 0 AND status = 'credited'`, accountID); n != 1 {
		t.Fatalf("got %d support payments at 0 F, want 1", n)
	}
	// The audit row goes through the outbox, a moment later.
	deadline := time.Now().Add(10 * time.Second)
	for countRows(t, `SELECT count(*) FROM audit_logs WHERE action = 'PASS_GRANTED_BY_SUPPORT' AND target_id = $1`, accountID) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the grant was never audited")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The owner still cannot grant themselves a pass.
	if code, _ = w.owner.call(http.MethodPost, "/api/v1/admin/passes/grant", map[string]string{
		"accountId": accountID, "passCode": "max_12m", "reason": "moi-même",
	}); code != http.StatusForbidden {
		t.Fatalf("grant as the owner = %d, want 403", code)
	}
}
