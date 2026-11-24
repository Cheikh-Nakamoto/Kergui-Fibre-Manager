package e2e

import (
	"context"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_Payments is issue #79: the « Paiements » page. An admin reads the
// revenue of a known set of payments by day and by month, by pass and by
// tier (credited only, gifts left out); sees the order stuck pending and
// the failed one; re-checks the stuck order at NabooPay (the fake gateway
// says paid) and it is credited, the gesture in the action log; and exports
// the payments of a date range as CSV. A household owner is refused all of
// it.
func TestE2E_Payments(t *testing.T) {
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	number := "+221" + local
	naboo := newFakeNabooPay(t)
	w := newWorldWith(t, fastTimings, withPhoneOwner(number), withNabooPay(naboo))
	box := "PAY" + local

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

	// The known set, in a month of the past nobody else writes to (the
	// database is shared between runs): two days of one month, one of the
	// next, plus what revenue leaves out.
	year, month := 1990+mathrand.IntN(30), time.Month(1+mathrand.IntN(11))
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(year, m, d, h, min, 0, 0, time.UTC) }
	next := month + 1
	type seed struct {
		ref, provider, pass, status string
		amount                      int64
		created                     time.Time
		credited                    *time.Time
	}
	ptr := func(t time.Time) *time.Time { return &t }
	run := uuid.NewString()[:8]
	seeds := []seed{
		{"r1-" + run, "naboopay", "basic_1m", "credited", 1000, at(month, 3, 8, 59), ptr(at(month, 3, 9, 0))},
		{"r2-" + run, "naboopay", "max_3m", "credited", 5700, at(month, 3, 23, 29), ptr(at(month, 3, 23, 30))},
		{"r3-" + run, "naboopay", "max_1m", "credited", 2000, at(month, 10, 11, 59), ptr(at(month, 10, 12, 0))},
		{"r4-" + run, "naboopay", "basic_1m", "credited", 1000, at(next, 2, 7, 59), ptr(at(next, 2, 8, 0))},
		// A gift (0 F), a failed order and a pending one: no revenue.
		{"r5-" + run, "support", "max_1m", "credited", 0, at(month, 3, 10, 0), ptr(at(month, 3, 10, 0))},
		{"r6-" + run, "naboopay", "max_1m", "failed", 2000, at(month, 3, 11, 0), nil},
		{"r7-" + run, "naboopay", "basic_3m", "pending", 2700, at(month, 3, 12, 0), nil},
	}
	ids := map[string]string{}
	insert := func(ref, provider, pass, status string, amount int64, created time.Time, credited *time.Time) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO hub_licensing_payments
			(id, provider, provider_ref, account_id, box_id, pass_code, amount_xof, status, created_at, credited_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			id, provider, ref, accountID, box, pass, amount, status, created, credited); err != nil {
			t.Fatal(err)
		}
		ids[ref] = id
		return id
	}
	for _, s := range seeds {
		insert(s.ref, s.provider, s.pass, s.status, s.amount, s.created, s.credited)
	}
	t.Cleanup(func() {
		list := make([]string, 0, len(ids))
		for _, id := range ids {
			list = append(list, id)
		}
		_, _ = pool.Exec(context.Background(), `DELETE FROM hub_licensing_payments WHERE id = ANY($1::uuid[])`, list)
	})

	// Now: an order stuck pending for half an hour (the webhook got lost),
	// a fresh one, and one that failed this morning.
	now := time.Now().UTC()
	stuckRef, freshRef, failedRef := "ord-stuck-"+run, "ord-fresh-"+run, "ord-failed-"+run
	stuckID := insert(stuckRef, "naboopay", "max_1m", "pending", 2000, now.Add(-30*time.Minute), nil)
	insert(freshRef, "naboopay", "basic_1m", "pending", 1000, now.Add(-time.Minute), nil)
	insert(failedRef, "naboopay", "max_1m", "failed", 2000, now.Add(-2*time.Hour), nil)
	giftID := ids["r5-"+run]

	day := func(m time.Month, d int) string { return at(m, d, 0, 0).Format("2006-01-02") }
	from, to := day(month, 1), day(next, 28)
	base := "/api/v1/admin/payments"

	// Nobody but a platform admin: anonymous 401, the owner 403.
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, base + "/revenue?from=" + from + "&to=" + to, nil},
		{http.MethodGet, base + "/stuck?olderThan=10m", nil},
		{http.MethodPost, base + "/" + stuckID + "/recheck", map[string]any{"reason": "moi-même"}},
		{http.MethodGet, base + "/export.csv?from=" + from + "&to=" + to, nil},
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

	email, password := "payments-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	// 1. Revenue by day: credited only, gifts out, the day's end included.
	code, body = ops.call(http.MethodGet, base+"/revenue?by=day&from="+from+"&to="+to, nil)
	if code != http.StatusOK {
		t.Fatalf("revenue by day = %d %v", code, body)
	}
	wantFigures(t, "totals", body["totals"], 9700, 4)
	wantPeriods(t, body, map[string][2]int64{
		day(month, 3): {6700, 2}, day(month, 10): {2000, 1}, day(next, 2): {1000, 1},
	})
	wantBreakdown(t, body, "byPass", "pass", map[string][2]int64{"basic_1m": {2000, 2}, "max_3m": {5700, 1}, "max_1m": {2000, 1}})
	wantBreakdown(t, body, "byTier", "tier", map[string][2]int64{"basic": {2000, 2}, "max": {7700, 2}})
	// A period carries its own split by pass and tier.
	for _, p := range body["periods"].([]any) {
		pm := p.(map[string]any)
		if str(pm, "period") == day(month, 3) {
			wantBreakdown(t, pm, "byTier", "tier", map[string][2]int64{"basic": {1000, 1}, "max": {5700, 1}})
		}
	}

	// By month.
	code, body = ops.call(http.MethodGet, base+"/revenue?by=month&from="+from+"&to="+to, nil)
	if code != http.StatusOK {
		t.Fatalf("revenue by month = %d %v", code, body)
	}
	wantFigures(t, "totals", body["totals"], 9700, 4)
	wantPeriods(t, body, map[string][2]int64{
		at(month, 1, 0, 0).Format("2006-01"): {8700, 3}, at(next, 1, 0, 0).Format("2006-01"): {1000, 1},
	})

	// One day, its last minute included.
	code, body = ops.call(http.MethodGet, base+"/revenue?by=day&from="+day(month, 3)+"&to="+day(month, 3), nil)
	if code != http.StatusOK {
		t.Fatalf("revenue of one day = %d %v", code, body)
	}
	wantFigures(t, "totals", body["totals"], 6700, 2)
	for _, bad := range []string{"?by=week", "?from=hier", "?from=" + to + "&to=" + from} {
		if code, _ = ops.call(http.MethodGet, base+"/revenue"+bad, nil); code != http.StatusBadRequest {
			t.Fatalf("revenue%s = %d, want 400", bad, code)
		}
	}

	// 2. What is stuck: pending for more than 10 minutes, and failed.
	refs := func(body map[string]any, key string) map[string]map[string]any {
		out := map[string]map[string]any{}
		list, _ := body[key].([]any)
		for _, p := range list {
			pm := p.(map[string]any)
			out[str(pm, "reference")] = pm
		}
		return out
	}
	code, body = ops.call(http.MethodGet, base+"/stuck?olderThan=10m", nil)
	if code != http.StatusOK {
		t.Fatalf("stuck = %d %v", code, body)
	}
	pending, failed := refs(body, "pending"), refs(body, "failed")
	if p := pending[stuckRef]; p == nil || str(p, "phone") != number || str(p, "tier") != "max" || p["recheckable"] != true {
		t.Fatalf("the stuck order is not listed as such: %v", body["pending"])
	}
	if pending[freshRef] != nil {
		t.Fatalf("a 1-minute-old order is listed as stuck: %v", body["pending"])
	}
	if failed[failedRef] == nil || failed["r6-"+run] != nil {
		t.Fatalf("failed = %v, want this morning's failure and not the old one", body["failed"])
	}
	if code, _ = ops.call(http.MethodGet, base+"/stuck?olderThan=bientôt", nil); code != http.StatusBadRequest {
		t.Fatalf("stuck with a bad duration = %d, want 400", code)
	}

	// 3. Re-check: NabooPay now says paid; the order is credited and the
	// pass runs on the box.
	naboo.order(stuckRef, "paid", 2000)
	if code, _ = ops.call(http.MethodPost, base+"/"+uuid.NewString()+"/recheck", map[string]any{}); code != http.StatusNotFound {
		t.Fatalf("recheck of an unknown payment = %d, want 404", code)
	}
	if code, _ = ops.call(http.MethodPost, base+"/pas-un-id/recheck", map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("recheck of a bad id = %d, want 400", code)
	}
	if code, _ = ops.call(http.MethodPost, base+"/"+giftID+"/recheck", map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("recheck of a gift = %d, want 400 (not a NabooPay order)", code)
	}
	const reason = "le client a payé, webhook perdu"
	code, body = ops.call(http.MethodPost, base+"/"+stuckID+"/recheck", map[string]any{"reason": reason})
	if code != http.StatusOK || str(body, "status") != "credited" || str(body, "previousStatus") != "pending" || body["credited"] != true {
		t.Fatalf("recheck = %d %v", code, body)
	}
	var tier string
	var expires time.Time
	if err := pool.QueryRow(ctx, `SELECT tier, expires_at FROM hub_licensing_subscriptions WHERE account_id = $1 AND box_id = $2`,
		accountID, box).Scan(&tier, &expires); err != nil || tier != "max" || !expires.After(now.Add(27*24*time.Hour)) {
		t.Fatalf("the box's pass after the recheck = %q %v %v", tier, expires, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM hub_licensing_subscriptions WHERE account_id = $1 AND box_id = $2`, accountID, box)
	})
	code, body = ops.call(http.MethodGet, base+"/stuck?olderThan=10m", nil)
	if code != http.StatusOK || refs(body, "pending")[stuckRef] != nil {
		t.Fatalf("the credited order is still stuck: %d %v", code, body["pending"])
	}
	// Again: nothing left to do, nothing credited twice.
	code, body = ops.call(http.MethodPost, base+"/"+stuckID+"/recheck", nil)
	if code != http.StatusOK || str(body, "status") != "credited" || body["credited"] != false {
		t.Fatalf("second recheck = %d %v", code, body)
	}
	entry := waitLogEntry(t, ops, email, "PAYMENT_RECHECKED")
	if str(entry.target(), "type") != "payment" || str(entry.target(), "id") != stuckID || str(entry, "tenantId") != w.owner.tenantID {
		t.Fatalf("recheck log entry = %v", entry)
	}
	found := false
	code, body = ops.call(http.MethodGet, "/api/v1/admin/audit?admin="+email+"&kind=PAYMENT_RECHECKED", nil)
	for _, e := range body["entries"].([]any) {
		if str(e.(map[string]any), "reason") == reason {
			found = true
		}
	}
	if code != http.StatusOK || !found {
		t.Fatalf("no recheck entry with the reason: %d %v", code, body)
	}

	// 4. CSV: an Excel-friendly BOM, the header, every payment created in
	// the range, oldest first, the full number.
	csvBody := func(path string) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, w.hub.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+ops.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, csv := csvBody(base + "/export.csv?from=" + from + "&to=" + to)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export = %d %v\n%s", resp.StatusCode, resp.Header, csv)
	}
	if !strings.HasPrefix(csv, "\uFEFF") {
		t.Fatalf("export has no BOM: %q", csv[:min(20, len(csv))])
	}
	lines := strings.Split(strings.TrimRight(strings.TrimPrefix(csv, "\uFEFF"), "\r\n"), "\r\n")
	const header = "created_at,credited_at,reference,provider,phone,box,pass,tier,amount_xof,status"
	if lines[0] != header {
		t.Fatalf("header = %q", lines[0])
	}
	mine := []string{}
	for _, l := range lines[1:] {
		if strings.Contains(l, run) {
			mine = append(mine, l)
		}
	}
	want := []string{
		fmt.Sprintf("%s,%s,r1-%s,naboopay,%s,%s,basic_1m,basic,1000,credited", rfc(at(month, 3, 8, 59)), rfc(at(month, 3, 9, 0)), run, number, box),
		fmt.Sprintf("%s,%s,r5-%s,support,%s,%s,max_1m,max,0,credited", rfc(at(month, 3, 10, 0)), rfc(at(month, 3, 10, 0)), run, number, box),
		fmt.Sprintf("%s,,r6-%s,naboopay,%s,%s,max_1m,max,2000,failed", rfc(at(month, 3, 11, 0)), run, number, box),
		fmt.Sprintf("%s,,r7-%s,naboopay,%s,%s,basic_3m,basic,2700,pending", rfc(at(month, 3, 12, 0)), run, number, box),
		fmt.Sprintf("%s,%s,r2-%s,naboopay,%s,%s,max_3m,max,5700,credited", rfc(at(month, 3, 23, 29)), rfc(at(month, 3, 23, 30)), run, number, box),
		fmt.Sprintf("%s,%s,r3-%s,naboopay,%s,%s,max_1m,max,2000,credited", rfc(at(month, 10, 11, 59)), rfc(at(month, 10, 12, 0)), run, number, box),
		fmt.Sprintf("%s,%s,r4-%s,naboopay,%s,%s,basic_1m,basic,1000,credited", rfc(at(next, 2, 7, 59)), rfc(at(next, 2, 8, 0)), run, number, box),
	}
	if strings.Join(mine, "\n") != strings.Join(want, "\n") {
		t.Fatalf("export rows:\n%s\nwant:\n%s", strings.Join(mine, "\n"), strings.Join(want, "\n"))
	}
	if resp, _ = csvBody(base + "/export.csv?from=" + to + "&to=" + from); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("export of a reversed range = %d, want 400", resp.StatusCode)
	}
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// wantFigures checks an {amountXof, count} pair.
func wantFigures(t *testing.T, what string, v any, amount, count int64) {
	t.Helper()
	m, _ := v.(map[string]any)
	a, _ := m["amountXof"].(float64)
	c, _ := m["count"].(float64)
	if int64(a) != amount || int64(c) != count {
		t.Fatalf("%s = %v, want %d F in %d payment(s)", what, v, amount, count)
	}
}

func wantPeriods(t *testing.T, body map[string]any, want map[string][2]int64) {
	t.Helper()
	periods, _ := body["periods"].([]any)
	if len(periods) != len(want) {
		t.Fatalf("periods = %v, want %v", periods, want)
	}
	prev := ""
	for _, p := range periods {
		pm := p.(map[string]any)
		key := str(pm, "period")
		w, ok := want[key]
		if !ok || key <= prev {
			t.Fatalf("periods = %v, want %v in order", periods, want)
		}
		prev = key
		wantFigures(t, "period "+key, pm, w[0], w[1])
	}
}

func wantBreakdown(t *testing.T, body map[string]any, key, label string, want map[string][2]int64) {
	t.Helper()
	list, _ := body[key].([]any)
	if len(list) != len(want) {
		t.Fatalf("%s = %v, want %v", key, list, want)
	}
	for _, e := range list {
		em := e.(map[string]any)
		w, ok := want[str(em, label)]
		if !ok {
			t.Fatalf("%s = %v, want %v", key, list, want)
		}
		wantFigures(t, key+" "+str(em, label), em, w[0], w[1])
	}
}
