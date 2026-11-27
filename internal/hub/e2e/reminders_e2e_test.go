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
	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// TestE2E_RenewalReminders is issue #99 through the whole hub: a household's
// pass gets its J-3 then its J0 reminder, by WhatsApp with the renewal link
// and as a push filed in the app's notifications (with no link), once each;
// the « Passes et paiements » expiring list shows them in its « rappels
// envoyés » column; and the back-office's switch, logged, stops them.
func TestE2E_RenewalReminders(t *testing.T) {
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	number := "+221" + local
	// The hub runs on real time for the sign-ins and the back-office; the
	// sweeps below move it to the day's hours in Dakar, then back.
	clock := &fakeClock{now: time.Now().UTC()}
	w := newWorldWith(t, fastTimings, withPhoneOwner(number), withClock(clock))
	realTime := func() { clock.Set(time.Now().UTC()) }

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
	var account string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM hub_licensing_accounts WHERE phone = $1`, number).Scan(&account); err != nil {
		t.Fatal(err)
	}
	// The switch is one row shared by every test of this database: start
	// and leave it as it was, on.
	exec(`DELETE FROM hub_settings WHERE key = $1`, admin.SettingReminders)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM hub_settings WHERE key = $1`, admin.SettingReminders)
	})
	token := "ExponentPushToken[rr-" + local + "]"
	if code, body := w.owner.call(http.MethodPut, w.owner.tenantPath("push-tokens"),
		map[string]any{"token": token, "platform": "android"}); code != http.StatusNoContent {
		t.Fatalf("PUT push-tokens = %d %v", code, body)
	}

	// A Max pass ending in three days, at 10:00 in Dakar.
	box, other := "RR"+local+"A", "RR"+local+"B"
	tomorrow := licensingdomain.DayStart(time.Now()).Add(24*time.Hour + 10*time.Hour)
	end := tomorrow.Add(48 * time.Hour)
	exec(`INSERT INTO hub_licensing_subscriptions (account_id, box_id, tier, expires_at) VALUES ($1, $2, 'max', $3)`, account, box, end)

	// sweep runs the reminders at the given time until nothing is left (the
	// shared database may hold other runs' passes ending the same days) and
	// returns the WhatsApp messages and pushes this household got.
	sweep := func(at time.Time) (messages []string, pushes []map[string]string) {
		t.Helper()
		clock.Set(at)
		defer realTime()
		for i := 0; i < 20; i++ {
			n, err := w.app.Licensing().Service.SendReminders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
		}
		for _, s := range w.notifier.Sent() {
			if string(s.To) == number && strings.Contains(s.Text, "box="+box) {
				messages = append(messages, s.Text)
			}
		}
		for _, p := range w.pusher.Pushed() {
			if p.Target.Token == token {
				pushes = append(pushes, p.Msg.Data)
			}
		}
		return messages, pushes
	}

	// At night, nothing; at 10:00 the day after tomorrow, the J-3 reminder.
	if msgs, _ := sweep(tomorrow.Add(-10*time.Hour + 3*time.Hour)); len(msgs) != 0 {
		t.Fatalf("reminded at night: %q", msgs)
	}
	msgs, pushes := sweep(tomorrow)
	link := "http://hub.test/licensing/v1/buy?box=" + box + "&pass=max_1m"
	if len(msgs) != 1 || !strings.Contains(msgs[0], "votre pass Max se termine le "+end.Format("02/01")+" à 10h00") || !strings.HasSuffix(msgs[0], link) {
		t.Fatalf("J-3 WhatsApp %q, want one with %s", msgs, link)
	}
	if len(pushes) != 1 || pushes[0]["kind"] != "announcement" || pushes[0]["title"] != "Votre pass se termine bientôt" ||
		strings.Contains(pushes[0]["body"], "http") || pushes[0]["body"] != "Votre pass Max se termine le "+end.Format("02/01")+" à 10h00." {
		t.Fatalf("J-3 push %v", pushes)
	}
	// Once: a sweep later the same day, or a hub restarted, sends nothing.
	if msgs, pushes := sweep(tomorrow.Add(2 * time.Hour)); len(msgs) != 1 || len(pushes) != 1 {
		t.Fatalf("J-3 twice: %q %v", msgs, pushes)
	}
	// J0, the morning of the day it ends.
	msgs, pushes = sweep(end.Add(-30 * time.Minute))
	if len(msgs) != 2 || !strings.Contains(msgs[1], "se termine aujourd'hui à 10h00") || len(pushes) != 2 ||
		pushes[1]["title"] != "Votre pass se termine aujourd'hui" {
		t.Fatalf("J0: %q %v", msgs, pushes)
	}

	// The back-office: admins only.
	settings := "/api/v1/admin/settings/renewal-reminders"
	anon := &ownerApp{t: t, hub: w.hub}
	if code, _ := anon.call(http.MethodGet, settings, nil); code != http.StatusUnauthorized {
		t.Fatalf("GET settings anonymously = %d", code)
	}
	if code, _ := w.owner.call(http.MethodPut, settings, map[string]any{"enabled": false}); code != http.StatusForbidden {
		t.Fatalf("PUT settings as the owner = %d", code)
	}
	email, password := "reminders-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	// The « rappels envoyés » column: J-3 then J0, each by WhatsApp and
	// in the app, with the phones it reached.
	code, body = ops.call(http.MethodGet, "/api/v1/admin/passes/expiring?q="+local, nil)
	if code != http.StatusOK {
		t.Fatalf("expiring = %d %v", code, body)
	}
	if s, _ := body["reminders"].(map[string]any); s["enabled"] != true || s["fromHour"] != float64(licensingdomain.ReminderFromHour) {
		t.Fatalf("the switch in the expiring list: %v", body["reminders"])
	}
	var row map[string]any
	for _, p := range body["passes"].([]any) {
		if m := p.(map[string]any); str(m, "boxId") == box {
			row = m
		}
	}
	sent, _ := row["reminders"].([]any)
	if len(sent) != 2 {
		t.Fatalf("reminders of %s: %v", box, row)
	}
	for i, step := range []string{"j3", "j0"} {
		r := sent[i].(map[string]any)
		if r["step"] != step || r["whatsapp"] != "sent" || r["push"] != "sent" || r["pushPhones"] != float64(1) || r["pushSent"] != float64(1) || str(r, "sentAt") == "" {
			t.Errorf("reminder %d: %v", i, r)
		}
	}

	// Switched off: logged, and nothing goes out for another pass.
	if code, _ := ops.call(http.MethodPut, settings, map[string]any{}); code != http.StatusBadRequest {
		t.Fatalf("PUT without enabled = %d, want 400", code)
	}
	code, body = ops.call(http.MethodPut, settings, map[string]any{"enabled": false})
	if code != http.StatusOK || body["enabled"] != false || str(body, "updatedAt") == "" {
		t.Fatalf("switch off = %d %v", code, body)
	}
	if code, body := ops.call(http.MethodGet, settings, nil); code != http.StatusOK || body["enabled"] != false {
		t.Fatalf("GET settings = %d %v", code, body)
	}
	waitForRow(t, 10*time.Second, `SELECT count(*) FROM audit_logs WHERE action = 'RENEWAL_REMINDERS_SET' AND target_type = 'settings'
		AND metadata->>'enabled' = 'false'`)
	exec(`INSERT INTO hub_licensing_subscriptions (account_id, box_id, tier, expires_at) VALUES ($1, $2, 'basic', $3)`, account, other, end)
	clock.Set(end.Add(-30 * time.Minute))
	n, err := w.app.Licensing().Service.SendReminders(context.Background())
	realTime()
	if err != nil || n != 0 {
		t.Fatalf("switched off: %d sent, %v", n, err)
	}
	var logged int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hub_licensing_reminders WHERE account_id = $1 AND box_id = $2`, account, other).Scan(&logged); err != nil || logged != 0 {
		t.Fatalf("switched off, %d reminders logged (%v)", logged, err)
	}
	// Back on, the reminder still due goes out.
	if code, body := ops.call(http.MethodPut, settings, map[string]any{"enabled": true}); code != http.StatusOK || body["enabled"] != true {
		t.Fatalf("switch on = %d %v", code, body)
	}
	sweep(end.Add(-30 * time.Minute))
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hub_licensing_reminders WHERE account_id = $1 AND box_id = $2 AND step = 'j0'`, account, other).Scan(&logged); err != nil || logged != 1 {
		t.Fatalf("switched back on, %d reminders logged (%v)", logged, err)
	}
}
