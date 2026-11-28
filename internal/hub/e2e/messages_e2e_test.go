package e2e

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_Announcements is issue #80 (parent #67, user stories 14, 15, 18,
// 19): « Messages » by push. Three households sign in with their numbers and
// register their app's push token: one on Max, one on Basic (whose expired
// Max box does not count), one on Gratuit. An admin previews each audience
// (households and phones counted), sends to one household by its number, to
// a level, and to everyone; the fake push records each announcement for the
// right households only, marked as an announcement for the app's inbox. The
// history carries the counters, the action log every send, and a household
// owner is refused all of it.
func TestE2E_Announcements(t *testing.T) {
	local := fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	numberMax := "+221" + local
	w := newWorldWith(t, fastTimings, withPhoneOwner(numberMax))
	pool := e2ePool(t)
	ctx := context.Background()

	// The two other households, each its own number.
	numberBasic := "+221" + fmt.Sprintf("78%07d", mathrand.IntN(10_000_000))
	numberFree := "+221" + fmt.Sprintf("76%07d", mathrand.IntN(10_000_000))
	basicIn := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.81", numberBasic, "basic phone")
	freeIn := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.82", numberFree, "free phone")
	basic := &ownerApp{t: t, hub: w.hub, token: basicIn.access, tenantID: basicIn.tenantID, userID: basicIn.userID}
	free := &ownerApp{t: t, hub: w.hub, token: freeIn.access, tenantID: freeIn.tenantID, userID: freeIn.userID}

	// Each app registers its push token, as it does once signed in.
	run := local
	tokenMax := "ExponentPushToken[msg-max-" + run + "]"
	tokenBasic := "ExponentPushToken[msg-basic-" + run + "]"
	tokenFree := "ExponentPushToken[msg-free-" + run + "]"
	for _, r := range []struct {
		app   *ownerApp
		token string
	}{{w.owner, tokenMax}, {basic, tokenBasic}, {free, tokenFree}} {
		if code, body := r.app.call("PUT", r.app.tenantPath("push-tokens"),
			map[string]any{"token": r.token, "platform": "android"}); code != http.StatusNoContent {
			t.Fatalf("PUT push-tokens = %d %v", code, body)
		}
	}

	// The levels: the highest active pass among the household's boxes.
	sub := func(number, box, tier string, expires time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO hub_licensing_subscriptions (account_id, box_id, tier, expires_at)
			SELECT id, $2, $3, $4 FROM hub_licensing_accounts WHERE phone = $1`,
			number, box, tier, expires); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	sub(numberMax, "MSGA1"+run, "basic", now.Add(20*24*time.Hour))
	sub(numberMax, "MSGA2"+run, "max", now.Add(10*24*time.Hour))
	sub(numberBasic, "MSGB1"+run, "basic", now.Add(30*24*time.Hour))
	sub(numberBasic, "MSGB2"+run, "max", now.Add(-24*time.Hour)) // ended: does not count
	sub(numberFree, "MSGC1"+run, "basic", now.Add(-time.Hour))   // ended

	const base = "/api/v1/admin/messages"
	announcement := map[string]any{"title": "Coupure prévue", "body": "Maintenance du réseau ce soir.", "audience": "all"}

	// Nobody but a platform admin: anonymous 401, the owner 403.
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, base + "/preview", map[string]any{"audience": "all"}},
		{http.MethodPost, base, announcement},
		{http.MethodGet, base, nil},
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
	for _, p := range w.pusher.Pushed() {
		if p.Msg.Data["kind"] == "announcement" && strings.Contains(p.Target.Token, run) {
			t.Fatalf("a refused send pushed anyway: %v", p)
		}
	}

	email, password := "messages-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	num := func(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }
	// pushedTo is which of this run's households an announcement reached.
	pushedTo := func(id string) map[string]int {
		out := map[string]int{}
		for _, p := range w.pusher.Pushed() {
			if p.Msg.Data["messageId"] != id {
				continue
			}
			switch p.Target.Token {
			case tokenMax:
				out["max"]++
			case tokenBasic:
				out["basic"]++
			case tokenFree:
				out["free"]++
			}
		}
		return out
	}
	// sentIDs is what this run sent, oldest first: the history check looks
	// at these only, the shared database keeps other runs' announcements.
	var sentIDs []string
	send := func(msg map[string]any) map[string]any {
		t.Helper()
		code, body := ops.call(http.MethodPost, base, msg)
		if code != http.StatusCreated {
			t.Fatalf("send %v = %d %v", msg, code, body)
		}
		if str(body, "messageId") == "" {
			t.Fatalf("send answered no message id: %v", body)
		}
		sentIDs = append(sentIDs, str(body, "messageId"))
		return body
	}
	same := func(name string, got map[string]int, want ...string) {
		t.Helper()
		exp := map[string]int{}
		for _, k := range want {
			exp[k] = 1
		}
		if fmt.Sprint(got) != fmt.Sprint(exp) {
			t.Fatalf("%s reached %v, want %v", name, got, exp)
		}
	}

	// 1. One household, by its number in local form: one household, one phone.
	code, body = ops.call(http.MethodPost, base+"/preview", map[string]any{"audience": "household", "value": local[:2] + " " + local[2:]})
	if code != http.StatusOK || num(body, "households") != 1 || num(body, "phones") != 1 {
		t.Fatalf("preview of one household = %d %v", code, body)
	}
	if h, _ := body["household"].(map[string]any); h == nil || str(h, "tenantId") != w.owner.tenantID {
		t.Fatalf("the preview does not name the household: %v", body)
	}
	one := send(map[string]any{"title": "Bonjour", "body": "Un mot pour vous.", "audience": "household", "value": numberBasic})
	if num(one, "phones") != 1 || num(one, "sent") != 1 || num(one, "failed") != 0 {
		t.Fatalf("send to one household = %v", one)
	}
	same("the household announcement", pushedTo(str(one, "messageId")), "basic")
	// The push is what the app files in its notifications.
	for _, p := range w.pusher.Pushed() {
		if p.Msg.Data["messageId"] == str(one, "messageId") {
			d := p.Msg.Data
			if d["kind"] != "announcement" || d["title"] != "Bonjour" || d["body"] != "Un mot pour vous." || d["tenantId"] != basic.tenantID {
				t.Fatalf("push data = %v", d)
			}
			if p.Msg.Title != "Bonjour" || p.Msg.Body != "Un mot pour vous." || p.Msg.Category != "" {
				t.Fatalf("push = %+v", p.Msg)
			}
		}
	}
	// By its id too.
	byID := send(map[string]any{"title": "Par id", "body": "Test.", "audience": "household", "value": free.tenantID})
	same("the announcement by id", pushedTo(str(byID, "messageId")), "free")

	// 2. A level: the highest active pass of the household's boxes.
	for tier, who := range map[string]string{"max": "max", "basic": "basic", "free": "free"} {
		code, prev := ops.call(http.MethodPost, base+"/preview", map[string]any{"audience": "tier", "value": tier})
		if code != http.StatusOK || num(prev, "households") < 1 || num(prev, "phones") < 1 {
			t.Fatalf("preview of %s = %d %v", tier, code, prev)
		}
		got := send(map[string]any{"title": "Niveau " + tier, "body": "Pour le niveau " + tier + ".", "audience": "tier", "value": tier})
		same("the "+tier+" announcement", pushedTo(str(got, "messageId")), who)
		if num(got, "sent")+num(got, "failed") != num(got, "phones") || num(got, "phones") < 1 {
			t.Fatalf("%s counters = %v", tier, got)
		}
	}

	// 3. Everyone.
	code, prev := ops.call(http.MethodPost, base+"/preview", map[string]any{"audience": "all"})
	if code != http.StatusOK || num(prev, "phones") < 3 || num(prev, "households") < num(prev, "householdsWithApp") || num(prev, "householdsWithApp") < 3 {
		t.Fatalf("preview of everyone = %d %v", code, prev)
	}
	all := send(announcement)
	same("the announcement to all", pushedTo(str(all, "messageId")), "max", "basic", "free")
	if num(all, "sent") < 3 || num(all, "sent")+num(all, "failed") != num(all, "phones") {
		t.Fatalf("counters of the announcement to all = %v", all)
	}

	// 4. What is refused.
	for _, bad := range []map[string]any{
		{"title": strings.Repeat("t", 81), "body": "x", "audience": "all"},
		{"title": "x", "body": strings.Repeat("b", 501), "audience": "all"},
		{"title": " ", "body": "x", "audience": "all"},
		{"title": "x", "body": "x", "audience": "everyone"},
		{"title": "x", "body": "x", "audience": "tier", "value": "pro"},
		{"title": "x", "body": "x", "audience": "household"},
		{"title": "x", "body": "x", "audience": "household", "value": "bonjour"},
	} {
		if code, body := ops.call(http.MethodPost, base, bad); code != http.StatusBadRequest {
			t.Fatalf("send %v = %d %v, want 400", bad, code, body)
		}
	}
	if code, body := ops.call(http.MethodPost, base+"/preview", map[string]any{"audience": "household", "value": "+221700000001"}); code != http.StatusNotFound {
		t.Fatalf("preview of an unknown number = %d %v, want 404", code, body)
	}

	// 5. The history, newest first, with its counters and author.
	dropFakeClockMessages(t)
	code, body = ops.call(http.MethodGet, base, nil)
	if code != http.StatusOK {
		t.Fatalf("history = %d %v", code, body)
	}
	list, _ := body["messages"].([]any)
	found := map[string]map[string]any{}
	var mine []string // this run's, in the history's order
	for _, m := range list {
		mm := m.(map[string]any)
		found[str(mm, "id")] = mm
		if slices.Contains(sentIDs, str(mm, "id")) {
			mine = append(mine, str(mm, "id"))
		}
	}
	h := found[str(one, "messageId")]
	if h == nil || str(h, "title") != "Bonjour" || str(h, "audience") != "household" || str(h, "channel") != "push" ||
		num(h, "phones") != 1 || num(h, "sent") != 1 || num(h, "failed") != 0 || num(h, "households") != 1 {
		t.Fatalf("history entry of the household announcement = %v", h)
	}
	if a, _ := h["author"].(map[string]any); a == nil || str(a, "email") != email {
		t.Fatalf("history author = %v", h["author"])
	}
	newestFirst := slices.Clone(sentIDs)
	slices.Reverse(newestFirst)
	if !slices.Equal(mine, newestFirst) || mine[0] != str(all, "messageId") {
		t.Fatalf("history is not newest first: this run's announcements read %v, sent %v", mine, sentIDs)
	}

	// 6. The action log: every send, with its message.
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM audit_logs WHERE action = 'ANNOUNCEMENT_SENT' AND target_type = 'message' AND target_id = $1 AND tenant_id = $2`,
		str(one, "messageId"), basic.tenantID)
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM audit_logs WHERE action = 'ANNOUNCEMENT_SENT' AND target_id = $1`, str(all, "messageId"))
	code, body = ops.call(http.MethodGet, "/api/v1/admin/audit?kind=ANNOUNCEMENT_SENT&limit=200", nil)
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(body["entries"]), "Coupure prévue") {
		t.Fatalf("the action log does not show the announcement: %d %v", code, body)
	}
	// And the deliveries, one row per phone.
	if n := countRows(t, `SELECT count(*) FROM hub_message_deliveries WHERE message_id = $1 AND status = 'sent'`, str(one, "messageId")); n != 1 {
		t.Fatalf("%d delivery rows for the household announcement, want 1", n)
	}
}

// dropFakeClockMessages removes the announcements dated ahead of now. The hub
// dates a message with its own clock, and the tests that run it on a fake one
// (the pass reminders) leave messages days ahead in the shared database: they
// sort before everything sent at the real time, and pile up run after run
// until a real announcement falls off the history's first page.
func dropFakeClockMessages(t *testing.T) {
	t.Helper()
	if _, err := e2ePool(t).Exec(context.Background(),
		`DELETE FROM hub_messages WHERE created_at > now() + interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
}
