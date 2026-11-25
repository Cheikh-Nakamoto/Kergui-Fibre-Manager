package e2e

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_PassRenewals is issue #97: the « Passes et paiements » page's
// expiring passes and renewal figures. An admin lists the passes ending
// within 7 days and those ended less than 30 days ago, soonest first, with
// the household and the last payment, filtered by soon / expired and by
// tier; then reads a past month's cohort: passes renewed (before or after
// their end, recorded by the licensing trigger), lost, and the trial that
// converted, whose end is no renewal date. A household owner is refused.
func TestE2E_PassRenewals(t *testing.T) {
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	number := "+221" + local
	w := newWorldWith(t, fastTimings, withPhoneOwner(number))

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var owner string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hub_licensing_accounts WHERE phone = $1`, number).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	// Two more numbers, each with its own trial (one per number).
	account := func() (id, local string) {
		t.Helper()
		id, local = uuid.NewString(), fmt.Sprintf("78%07d", mathrand.IntN(10_000_000))
		exec(`INSERT INTO hub_licensing_accounts (id, phone) VALUES ($1, $2)`, id, "+221"+local)
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM hub_licensing_accounts WHERE id = $1`, id) })
		return id, local
	}
	trialAcc, trialLocal := account()
	cohortTrialAcc, _ := account()

	run := uuid.NewString()[:6]
	box := func(name string) string { return "RN" + name + run }
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM hub_licensing_subscriptions WHERE account_id = $1`, owner)
		_, _ = pool.Exec(context.Background(), `DELETE FROM hub_licensing_renewals WHERE account_id = $1`, owner)
		_, _ = pool.Exec(context.Background(), `DELETE FROM hub_licensing_payments WHERE account_id = $1`, owner)
	})
	sub := func(acc, b, tier string, expires time.Time) {
		t.Helper()
		exec(`INSERT INTO hub_licensing_subscriptions (account_id, box_id, tier, expires_at) VALUES ($1, $2, $3, $4)`,
			acc, b, tier, expires)
	}
	renew := func(acc, b string, expires time.Time) {
		t.Helper()
		exec(`UPDATE hub_licensing_subscriptions SET expires_at = $3 WHERE account_id = $1 AND box_id = $2`, acc, b, expires)
	}
	paid := func(acc, pass, provider string, amount int64, at time.Time) {
		t.Helper()
		exec(`INSERT INTO hub_licensing_payments
			(id, provider, provider_ref, account_id, box_id, pass_code, amount_xof, status, created_at, credited_at)
			VALUES ($1, $2, $3, $4, NULL, $5, $6, 'credited', $7, $7)`,
			uuid.NewString(), provider, "rn-"+uuid.NewString(), acc, pass, amount, at)
	}
	trial := func(acc, phone, b string, started time.Time) {
		t.Helper()
		exec(`INSERT INTO hub_licensing_trials (phone, box_id, account_id, requested_at, started_at) VALUES ($1, $2, $3, $4, $4)`,
			phone, b, acc, started)
	}
	const day = 24 * time.Hour

	// --- the expiring list, around now -------------------------------------
	now := time.Now().UTC()
	sub(owner, box("A"), "max", now.Add(2*day))     // soon
	sub(owner, box("B"), "basic", now.Add(-5*day))  // ended lately
	sub(owner, box("C"), "basic", now.Add(-40*day)) // ended too long ago
	sub(owner, box("D"), "max", now.Add(20*day))    // not soon
	sub(owner, box("Z"), "free", now.Add(day))      // no pass
	paid(owner, "basic_1m", "naboopay", 1000, now.Add(-40*day))
	paid(owner, "max_1m", "naboopay", 2000, now.Add(-time.Hour))
	trial(trialAcc, "+221"+trialLocal, box("E"), now.Add(-6*day))
	sub(trialAcc, box("E"), "max", now.Add(-6*day).Add(7*day))

	base := "/api/v1/admin/passes"
	anon := &ownerApp{t: t, hub: w.hub}
	for _, p := range []string{base + "/expiring", base + "/renewals"} {
		if code, _ := anon.call(http.MethodGet, p, nil); code != http.StatusUnauthorized {
			t.Fatalf("GET %s anonymously = %d, want 401", p, code)
		}
		if code, _ := w.owner.call(http.MethodGet, p, nil); code != http.StatusForbidden {
			t.Fatalf("GET %s as the owner = %d, want 403", p, code)
		}
	}
	email, password := "renewals-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	passes := func(query string) []map[string]any {
		t.Helper()
		code, body := ops.call(http.MethodGet, base+"/expiring"+query, nil)
		if code != http.StatusOK {
			t.Fatalf("expiring%s = %d %v", query, code, body)
		}
		list, _ := body["passes"].([]any)
		out := []map[string]any{}
		for _, p := range list {
			out = append(out, p.(map[string]any))
		}
		return out
	}
	boxes := func(list []map[string]any) []string {
		out := []string{}
		for _, p := range list {
			out = append(out, str(p, "boxId"))
		}
		return out
	}
	wantBoxes := func(what string, list []map[string]any, want ...string) {
		t.Helper()
		if got := boxes(list); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}

	all := passes("?q=" + local)
	wantBoxes("expiring", all, box("B"), box("A"))
	b, a := all[0], all[1]
	if b["expired"] != true || str(b, "tier") != "basic" || a["expired"] != false || str(a, "tier") != "max" {
		t.Fatalf("expiring rows = %v", all)
	}
	if str(a, "phone") != number || str(a, "tenantId") != w.owner.tenantID || str(a, "household") == "" || a["trial"] != false {
		t.Fatalf("the household of a row = %v", a)
	}
	if lp, _ := a["lastPayment"].(map[string]any); lp == nil || str(lp, "passCode") != "max_1m" || str(lp, "tier") != "max" ||
		lp["amountXof"] != float64(2000) || str(lp, "provider") != "naboopay" {
		t.Fatalf("last payment = %v, want this hour's max_1m", a["lastPayment"])
	}
	wantBoxes("soon", passes("?status=soon&q="+local), box("A"))
	wantBoxes("expired", passes("?status=expired&q="+local), box("B"))
	wantBoxes("basic", passes("?tier=basic&q="+local), box("B"))
	wantBoxes("max soon", passes("?tier=max&status=soon&q="+local), box("A"))
	if e := passes("?q=" + trialLocal); len(e) != 1 || str(e[0], "boxId") != box("E") || e[0]["trial"] != true || e[0]["lastPayment"] != nil {
		t.Fatalf("the trial ending = %v", e)
	}
	for _, bad := range []string{"?status=bientot", "?tier=free"} {
		if code, _ := ops.call(http.MethodGet, base+"/expiring"+bad, nil); code != http.StatusBadRequest {
			t.Fatalf("expiring%s = %d, want 400", bad, code)
		}
	}

	// --- a past month's cohort ----------------------------------------------
	// A month nobody else writes to (the database is shared between runs);
	// what an interrupted run left there is cleared first.
	year, month := 1990+mathrand.IntN(30), time.Month(1+mathrand.IntN(12))
	at := func(d int) time.Time { return time.Date(year, month, d, 10, 0, 0, 0, time.UTC) }
	start, end := at(1).Add(-10*time.Hour), at(1).Add(-10*time.Hour).AddDate(0, 1, 0)
	exec(`DELETE FROM hub_licensing_subscriptions WHERE expires_at >= $1 AND expires_at < $2`, start, end)
	exec(`DELETE FROM hub_licensing_renewals WHERE due_at >= $1 AND due_at < $2`, start, end)
	exec(`DELETE FROM hub_licensing_trials WHERE started_at >= $1 AND started_at < $2`, start.Add(-8*day), end)

	later := now.Add(30 * day)
	sub(owner, box("F"), "basic", at(5)) // renewed after its end
	renew(owner, box("F"), later)
	sub(owner, box("G"), "max", at(10)) // lost
	sub(owner, box("I"), "max", at(20)) // renewed before its end
	renew(owner, box("I"), at(20).Add(30*day))
	renew(owner, box("I"), later) // the next end, in the month after: not this cohort
	sub(owner, box("Y"), "free", at(12))
	// A trial that ended on the 8th and converted on the 9th: its end is no
	// renewal date, neither renewed nor lost.
	trial(cohortTrialAcc, "+221"+fmt.Sprintf("79%07d", mathrand.IntN(10_000_000)), box("H"), at(1))
	sub(cohortTrialAcc, box("H"), "max", at(1).Add(7*day))
	paid(cohortTrialAcc, "max_1m", "naboopay", 2000, at(9))
	renew(cohortTrialAcc, box("H"), later)

	ym := at(1).Format("2006-01")
	code, body = ops.call(http.MethodGet, base+"/renewals?month="+ym, nil)
	if code != http.StatusOK || str(body, "month") != ym {
		t.Fatalf("renewals = %d %v", code, body)
	}
	r, _ := body["renewal"].(map[string]any)
	if r["renewed"] != float64(2) || r["lost"] != float64(1) || r["due"] != float64(3) || r["upcoming"] != float64(0) {
		t.Fatalf("renewal = %v, want 2 renewed and 1 lost of 3", r)
	}
	if rate, _ := r["rate"].(float64); rate < 0.66 || rate > 0.67 {
		t.Fatalf("rate = %v, want 2/3", r["rate"])
	}
	if tr, _ := body["trials"].(map[string]any); tr["ended"] != float64(1) || tr["converted"] != float64(1) {
		t.Fatalf("the month's trials = %v, want 1 ended and converted", body["trials"])
	}
	lost, _ := body["lost"].([]any)
	if len(lost) != 1 {
		t.Fatalf("lost = %v, want box G", lost)
	}
	if l := lost[0].(map[string]any); str(l, "boxId") != box("G") || str(l, "phone") != number || l["expired"] != true {
		t.Fatalf("lost = %v, want box G with the number", l)
	}
	if str(body, "trackedSince") == "" {
		t.Fatalf("no trackedSince: %v", body)
	}

	// Now: A, D and the trial E run; E is a trial.
	code, body = ops.call(http.MethodGet, base+"/renewals", nil)
	if code != http.StatusOK || str(body, "month") != now.Format("2006-01") {
		t.Fatalf("this month's renewals = %d %v", code, body)
	}
	if act, _ := body["active"].(map[string]any); act["max"].(float64) < 3 || act["maxTrial"].(float64) < 1 {
		t.Fatalf("active = %v, want at least A, D and the trial E", act)
	}
	if code, _ = ops.call(http.MethodGet, base+"/renewals?month=2026-13", nil); code != http.StatusBadRequest {
		t.Fatalf("renewals of month 13 = %d, want 400", code)
	}
}
