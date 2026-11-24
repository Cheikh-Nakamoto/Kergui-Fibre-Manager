package e2e

import (
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// TestE2E_Complaints is issue #82 (parent #68, user stories 2, 3, 6, 7, 8,
// 9, 18, 20, 21): the complaints module. A customer signed in to the app
// reports a problem with the app's journal; the hub stores it and
// acknowledges it with its number; the customer sees it with its thread,
// another customer does not and cannot write on it. An admin finds it in
// the back-office list, reads the thread and the journal, and answers: the
// fake push carries {kind: "reply", …} to the customer's phone only, and
// the WhatsApp queue the answer to the opted-in number. The customer
// answers back (the complaint opens again), the admin closes it, and it
// takes no more messages. Every write is in the action log; the admins'
// WhatsApp numbers are set and normalized; a household owner is refused
// every admin route.
func TestE2E_Complaints(t *testing.T) {
	numberA := "+221" + fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	w := newWorldWith(t, fastTimings, withPhoneOwner(numberA))
	ctx := context.Background()
	run := strings.TrimPrefix(numberA, "+221")

	numberB := "+221" + fmt.Sprintf("78%07d", mathrand.IntN(10_000_000))
	inB := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.131", numberB, "phone B")
	appB := &ownerApp{t: t, hub: w.hub, token: inB.access, tenantID: inB.tenantID, userID: inB.userID, session: inB.session}

	// Both apps register for notifications; A also activates WhatsApp.
	tokenA := "ExponentPushToken[complaint-a-" + run + "]"
	tokenB := "ExponentPushToken[complaint-b-" + run + "]"
	for _, r := range []struct {
		app   *ownerApp
		token string
	}{{w.owner, tokenA}, {appB, tokenB}} {
		if code, body := r.app.call("PUT", r.app.tenantPath("push-tokens"),
			map[string]any{"token": r.token, "platform": "android"}); code != http.StatusNoContent {
			t.Fatalf("PUT push-tokens = %d %v", code, body)
		}
	}
	optInAs(t, w, w.owner, alertsdomain.Phone(numberA))

	// Each call from its own address: the licensing routes share a budget
	// per address.
	ip := 0
	as := func(session, method, path string, in any) (int, map[string]any) {
		t.Helper()
		ip++
		return callFrom(t, w.hub.URL, fmt.Sprintf("198.51.100.%d", 140+ip%100), method, path, session, in)
	}
	num := func(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }
	const base = "/licensing/v1/complaints"

	// 1. Intake: anonymous refused, a bad category refused, then stored
	// and acknowledged with its number.
	if code, _ := as("", http.MethodPost, base, map[string]any{"category": "paiement", "message": "x"}); code != http.StatusUnauthorized {
		t.Fatalf("anonymous complaint = %d, want 401", code)
	}
	for _, bad := range []map[string]any{
		{"category": "réseau", "message": "Mon paiement n'est pas passé."},
		{"category": "paiement", "message": "  "},
		{"category": "paiement", "message": strings.Repeat("a", 2001)},
		{"category": "paiement", "message": "x", "source": "whatsapp"},
	} {
		if code, body := as(w.owner.session, http.MethodPost, base, bad); code != http.StatusBadRequest {
			t.Fatalf("complaint %v = %d %v, want 400", bad, code, body)
		}
	}
	journal := "line 1\n" + strings.Repeat("journal de l'app ", 5000) + "\nlast line " + run
	code, ack := as(w.owner.session, http.MethodPost, base, map[string]any{
		"category": "paiement", "message": "Mon paiement Wave " + run + " n'est pas arrivé.", "journal": journal,
	})
	if code != http.StatusCreated || num(ack, "number") <= 0 || str(ack, "status") != "open" ||
		str(ack, "source") != "app" || ack["hasJournal"] != true || str(ack, "id") == "" {
		t.Fatalf("complaint = %d %v", code, ack)
	}
	number := num(ack, "number")
	id := str(ack, "id")
	// The journal is capped, its most recent end kept.
	var stored string
	if err := e2ePool(t).QueryRow(ctx, `SELECT journal FROM hub_complaints WHERE id = $1`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 64<<10 || !strings.HasSuffix(stored, "last line "+run) {
		t.Fatalf("stored journal: %d bytes, ends %q", len(stored), stored[max(0, len(stored)-30):])
	}

	// 2. The customer sees it with its thread; the other customer does not.
	mine := func(session string) []map[string]any {
		t.Helper()
		code, body := as(session, http.MethodGet, base, nil)
		if code != http.StatusOK {
			t.Fatalf("GET complaints = %d %v", code, body)
		}
		var out []map[string]any
		list, _ := body["complaints"].([]any)
		for _, c := range list {
			out = append(out, c.(map[string]any))
		}
		return out
	}
	find := func(list []map[string]any) map[string]any {
		for _, c := range list {
			if str(c, "id") == id {
				return c
			}
		}
		return nil
	}
	got := find(mine(w.owner.session))
	if got == nil || num(got, "number") != number || str(got, "category") != "paiement" || got["journal"] != nil {
		t.Fatalf("the customer's complaint = %v", got)
	}
	if thread, _ := got["messages"].([]any); len(thread) != 1 ||
		str(thread[0].(map[string]any), "from") != "customer" || !strings.Contains(str(thread[0].(map[string]any), "body"), run) {
		t.Fatalf("thread = %v", got["messages"])
	}
	if find(mine(appB.session)) != nil {
		t.Fatal("another customer sees the complaint")
	}
	path := base + "/" + strconv.Itoa(number) + "/messages"
	if code, body := as(appB.session, http.MethodPost, path, map[string]any{"message": "Je m'invite."}); code != http.StatusNotFound {
		t.Fatalf("another customer writing on it = %d %v, want 404", code, body)
	}

	// 3. Nobody but a platform admin on the back-office routes.
	const adminBase = "/api/v1/admin/complaints"
	const adminsPath = "/api/v1/admin/settings/complaint-admins"
	routes := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, adminBase, nil},
		{http.MethodGet, adminBase + "/summary", nil},
		{http.MethodGet, adminBase + "/" + id, nil},
		{http.MethodPost, adminBase + "/" + id + "/answer", map[string]any{"message": "Bonjour"}},
		{http.MethodPost, adminBase + "/" + id + "/close", nil},
		{http.MethodGet, adminsPath, nil},
		{http.MethodPut, adminsPath, map[string]any{"numbers": []string{numberB}}},
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

	email, password := "complaints-"+run+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, e2ePool(t), email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")

	// 4. The back-office: the list, filtered, and the detail with journal.
	code, list := ops.call(http.MethodGet, adminBase+"?status=open&category=paiement&source=app&phone="+run, nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %v", code, list)
	}
	entries, _ := list["complaints"].([]any)
	if len(entries) != 1 || str(entries[0].(map[string]any), "id") != id {
		t.Fatalf("filtered list = %v", entries)
	}
	e := entries[0].(map[string]any)
	if str(e, "phone") != numberA || !strings.Contains(str(e, "preview"), run) || num(e, "messageCount") != 1 ||
		e["hasJournal"] != true || e["household"] == nil {
		t.Fatalf("list entry = %v", e)
	}
	if c, _ := list["counts"].(map[string]any); c == nil || num(c, "open") < 1 {
		t.Fatalf("counts = %v", list["counts"])
	}
	for _, q := range []string{"?category=box&phone=" + run, "?status=closed&phone=" + run, "?olderThanHours=24&phone=" + run} {
		code, other := ops.call(http.MethodGet, adminBase+q, nil)
		if l, _ := other["complaints"].([]any); code != http.StatusOK || len(l) != 0 {
			t.Fatalf("list %s = %d %v, want none", q, code, other)
		}
	}
	if code, body := ops.call(http.MethodGet, adminBase+"?status=pending", nil); code != http.StatusBadRequest {
		t.Fatalf("a bad status filter = %d %v, want 400", code, body)
	}
	code, det := ops.call(http.MethodGet, adminBase+"/"+strconv.Itoa(number), nil)
	if code != http.StatusOK {
		t.Fatalf("detail by number = %d %v", code, det)
	}
	c, _ := det["complaint"].(map[string]any)
	if c == nil || str(c, "id") != id || !strings.HasSuffix(str(c, "journal"), "last line "+run) {
		t.Fatalf("detail = %v", det)
	}
	if code, _ := ops.call(http.MethodGet, adminBase+"/999999999", nil); code != http.StatusNotFound {
		t.Fatalf("an unknown complaint = %d, want 404", code)
	}

	// 5. The answer: push « reply » to the customer's phone, WhatsApp to
	// the opted-in number, status answered.
	if code, body := ops.call(http.MethodPost, adminBase+"/"+id+"/answer", map[string]any{"message": " "}); code != http.StatusBadRequest {
		t.Fatalf("an empty answer = %d %v, want 400", code, body)
	}
	answer := "Bonjour, votre paiement " + run + " est crédité."
	code, ans := ops.call(http.MethodPost, adminBase+"/"+id+"/answer", map[string]any{"message": answer})
	if code != http.StatusCreated {
		t.Fatalf("answer = %d %v", code, ans)
	}
	d, _ := ans["delivery"].(map[string]any)
	if d == nil || num(d, "pushed") != 1 || num(d, "pushFailed") != 0 || str(d, "whatsapp") != "queued" {
		t.Fatalf("answer delivery = %v", ans["delivery"])
	}
	if ac, _ := ans["complaint"].(map[string]any); ac == nil || str(ac, "status") != "answered" {
		t.Fatalf("answered complaint = %v", ans["complaint"])
	}
	p := w.waitForPush(t, tokenA, 10*time.Second)
	data := p.Msg.Data
	if data["kind"] != "reply" || data["complaintId"] != id || data["number"] != strconv.Itoa(number) ||
		data["body"] != answer || data["category"] != "paiement" || data["tenantId"] != w.owner.tenantID {
		t.Fatalf("reply push data = %v", data)
	}
	if !strings.Contains(p.Msg.Title, strconv.Itoa(number)) || p.Msg.Body != answer {
		t.Fatalf("reply push = %+v", p.Msg)
	}
	for _, pp := range w.pusher.Pushed() {
		if pp.Target.Token == tokenB && pp.Msg.Data["kind"] == "reply" {
			t.Fatalf("the other customer got the answer: %v", pp.Msg.Data)
		}
	}
	wa := waitForText(t, w.notifier, alertsdomain.Phone(numberA), "plainte n° "+strconv.Itoa(number), 10*time.Second)
	if !strings.Contains(wa.Text, answer) {
		t.Fatalf("WhatsApp answer = %q", wa.Text)
	}
	// The customer reads it on the thread.
	got = find(mine(w.owner.session))
	if thread, _ := got["messages"].([]any); str(got, "status") != "answered" || len(thread) != 2 ||
		str(thread[1].(map[string]any), "from") != "admin" || str(thread[1].(map[string]any), "body") != answer ||
		thread[1].(map[string]any)["author"] != nil {
		t.Fatalf("the customer's thread after the answer = %v", got)
	}

	// 6. The customer answers back: the complaint opens again.
	code, back := as(w.owner.session, http.MethodPost, path, map[string]any{"message": "Merci, c'est bon."})
	if code != http.StatusCreated || str(back, "status") != "open" {
		t.Fatalf("customer reply = %d %v", code, back)
	}

	// 7. Closed: logged, and it takes nothing more.
	code, closed := ops.call(http.MethodPost, adminBase+"/"+id+"/close", nil)
	if cc, _ := closed["complaint"].(map[string]any); code != http.StatusOK || cc == nil || str(cc, "status") != "closed" {
		t.Fatalf("close = %d %v", code, closed)
	}
	if code, _ := ops.call(http.MethodPost, adminBase+"/"+id+"/close", nil); code != http.StatusConflict {
		t.Fatalf("closing twice = %d, want 409", code)
	}
	if code, _ := ops.call(http.MethodPost, adminBase+"/"+id+"/answer", map[string]any{"message": "Encore"}); code != http.StatusConflict {
		t.Fatalf("answering a closed complaint = %d, want 409", code)
	}
	if code, body := as(w.owner.session, http.MethodPost, path, map[string]any{"message": "Encore moi"}); code != http.StatusConflict {
		t.Fatalf("customer writing on a closed complaint = %d %v, want 409", code, body)
	}
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM audit_logs WHERE action = 'COMPLAINT_ANSWERED' AND target_type = 'complaint' AND target_id = $1 AND tenant_id = $2`,
		id, w.owner.tenantID)
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM audit_logs WHERE action = 'COMPLAINT_CLOSED' AND target_type = 'complaint' AND target_id = $1`, id)
	code, logged := ops.call(http.MethodGet, "/api/v1/admin/audit?kind=COMPLAINT_CLOSED&limit=200", nil)
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(logged["entries"]), "n° "+strconv.Itoa(number)) {
		t.Fatalf("the action log does not show the close: %d %v", code, logged)
	}

	// 8. A customer's budget: five new complaints an hour.
	for i := range 5 {
		if code, body := as(appB.session, http.MethodPost, base, map[string]any{"category": "box", "message": fmt.Sprintf("Box %d", i)}); code != http.StatusCreated {
			t.Fatalf("complaint %d of B = %d %v", i, code, body)
		}
	}
	if code, body := as(appB.session, http.MethodPost, base, map[string]any{"category": "box", "message": "Une de trop", "source": "web"}); code != http.StatusTooManyRequests {
		t.Fatalf("the sixth complaint in an hour = %d %v, want 429", code, body)
	}

	// 9. The admins' WhatsApp numbers: normalized, an invalid one refused,
	// logged.
	local := strings.TrimPrefix(numberB, "+221")
	// The admins' numbers are the hub's, not this test's: the next tests'
	// hubs would send them the complaints' summary (issue #83).
	t.Cleanup(func() { execSQL(t, `DELETE FROM hub_settings WHERE key = 'complaint_admins'`) })
	code, set := ops.call(http.MethodPut, adminsPath, map[string]any{"numbers": []string{local[:2] + " " + local[2:], "00" + strings.TrimPrefix(numberA, "+"), numberB, ""}})
	if code != http.StatusOK || fmt.Sprint(set["numbers"]) != fmt.Sprint([]any{numberB, numberA}) {
		t.Fatalf("set admins = %d %v", code, set)
	}
	if code, body := ops.call(http.MethodPut, adminsPath, map[string]any{"numbers": []string{"bonjour"}}); code != http.StatusBadRequest {
		t.Fatalf("an invalid admin number = %d %v, want 400", code, body)
	}
	code, read := ops.call(http.MethodGet, adminsPath, nil)
	if code != http.StatusOK || fmt.Sprint(read["numbers"]) != fmt.Sprint([]any{numberB, numberA}) {
		t.Fatalf("read admins = %d %v", code, read)
	}
	waitForRow(t, 10*time.Second, `SELECT count(*) FROM audit_logs WHERE action = 'COMPLAINT_ADMINS_SET' AND target_type = 'settings'`)
}
