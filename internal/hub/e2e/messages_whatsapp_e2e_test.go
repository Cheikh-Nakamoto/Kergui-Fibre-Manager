package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/app"
)

// TestE2E_WhatsAppMessages is issue #81 (parent #67): « Messages » by
// WhatsApp, to a household or a small hand-picked group, through a queue
// that sends one message every interval (40 s in production, 200 ms here).
// Two opted-in numbers receive it spaced by at least the interval; a number
// that sent STOP, one that never opted in and one that is not a number are
// refused and listed; 21 numbers are refused (400), a group with no
// sendable number too (409); a household owner is refused all of it (403);
// every send is logged; and a row still queued when the hub stops is sent
// by the hub that starts next.
func TestE2E_WhatsAppMessages(t *testing.T) {
	numberA := "+221" + fmt.Sprintf("77%07d", mathrand.IntN(10_000_000))
	w := newWorldWith(t, fastTimings, withPhoneOwner(numberA))
	ctx := context.Background()

	numberB := "+221" + fmt.Sprintf("78%07d", mathrand.IntN(10_000_000))
	numberC := "+221" + fmt.Sprintf("76%07d", mathrand.IntN(10_000_000))
	numberD := "+221" + fmt.Sprintf("75%07d", mathrand.IntN(10_000_000)) // never opts in
	inB := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.91", numberB, "phone B")
	inC := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.92", numberC, "phone C")
	appB := &ownerApp{t: t, hub: w.hub, token: inB.access, tenantID: inB.tenantID, userID: inB.userID}
	appC := &ownerApp{t: t, hub: w.hub, token: inC.access, tenantID: inC.tenantID, userID: inC.userID}

	// A, B and C activate WhatsApp; C then sends STOP.
	optInAs(t, w, w.owner, alertsdomain.Phone(numberA))
	optInAs(t, w, appB, alertsdomain.Phone(numberB))
	optInAs(t, w, appC, alertsdomain.Phone(numberC))
	w.send(t, alertsdomain.Phone(numberC), "STOP", "")

	const base = "/api/v1/admin/messages"
	local := strings.TrimPrefix(numberA, "+221")
	group := []string{local[:2] + " " + local[2:], "00" + strings.TrimPrefix(numberB, "+"), numberC, numberD, "bonjour"}
	run := local
	msg := map[string]any{"title": "Info " + run, "body": "Travaux dans votre quartier.", "channel": "whatsapp",
		"audience": "numbers", "numbers": group}

	// Nobody but a platform admin.
	for _, r := range []struct {
		path string
		body any
	}{{base + "/preview", msg}, {base, msg}} {
		if code, _ := w.owner.call(http.MethodPost, r.path, r.body); code != http.StatusForbidden {
			t.Fatalf("POST %s as the owner = %d, want 403", r.path, code)
		}
	}

	email, password := "wa-messages-"+run+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, e2ePool(t), email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")
	num := func(m map[string]any, k string) int { f, _ := m[k].(float64); return int(f) }
	refusedOf := func(m map[string]any) map[string]string {
		out := map[string]string{}
		list, _ := m["refused"].([]any)
		for _, r := range list {
			rr := r.(map[string]any)
			out[str(rr, "number")] = str(rr, "reason")
		}
		return out
	}
	wantRefused := map[string]string{numberC: "stopped", numberD: "not_opted_in", "bonjour": "invalid"}

	// 1. The preview: two numbers receive it, three are refused and why.
	code, prev := ops.call(http.MethodPost, base+"/preview", msg)
	if code != http.StatusOK || num(prev, "phones") != 2 || str(prev, "channel") != "whatsapp" ||
		fmt.Sprint(prev["recipients"]) != fmt.Sprint([]any{numberA, numberB}) {
		t.Fatalf("preview = %d %v", code, prev)
	}
	if got := refusedOf(prev); fmt.Sprint(got) != fmt.Sprint(wantRefused) {
		t.Fatalf("refused in the preview = %v, want %v", got, wantRefused)
	}
	if prev["intervalSeconds"].(float64) != whatsAppSendInterval.Seconds() || num(prev, "groupMax") != 20 {
		t.Fatalf("preview pace = %v", prev)
	}

	// 2. The send: queued, then sent one by one, spaced by the interval.
	code, sent := ops.call(http.MethodPost, base, msg)
	if code != http.StatusCreated || num(sent, "queued") != 2 || num(sent, "phones") != 2 || str(sent, "messageId") == "" {
		t.Fatalf("send = %d %v", code, sent)
	}
	if got := refusedOf(sent); fmt.Sprint(got) != fmt.Sprint(wantRefused) {
		t.Fatalf("refused in the send = %v", got)
	}
	id := str(sent, "messageId")
	gotA := waitForText(t, w.notifier, alertsdomain.Phone(numberA), "Info "+run, 10*time.Second)
	gotB := waitForText(t, w.notifier, alertsdomain.Phone(numberB), "Info "+run, 10*time.Second)
	first, second := gotA, gotB
	if second.At.Before(first.At) {
		first, second = second, first
	}
	if gap := second.At.Sub(first.At); gap < whatsAppSendInterval {
		t.Fatalf("two messages %v apart, want at least %v", gap, whatsAppSendInterval)
	}
	if !strings.Contains(gotA.Text, "Travaux dans votre quartier.") || !strings.Contains(gotA.Text, "STOP") || gotA.AlertID != "" {
		t.Fatalf("message = %+v", gotA)
	}
	for _, s := range w.notifier.Sent() {
		if strings.Contains(s.Text, "Info "+run) && s.To != alertsdomain.Phone(numberA) && s.To != alertsdomain.Phone(numberB) {
			t.Fatalf("a refused number was sent to: %s", s.To.Masked())
		}
	}
	waitForRow(t, 10*time.Second,
		`SELECT CASE WHEN count(*) = 2 THEN 1 ELSE 0 END FROM hub_message_deliveries WHERE message_id = $1 AND channel = 'whatsapp' AND status = 'sent'`, id)

	// The history: a WhatsApp message to numbers, nothing left in the queue.
	dropFakeClockMessages(t)
	code, hist := ops.call(http.MethodGet, base, nil)
	if code != http.StatusOK {
		t.Fatalf("history = %d %v", code, hist)
	}
	var entry map[string]any
	list, _ := hist["messages"].([]any)
	for _, m := range list {
		if mm := m.(map[string]any); str(mm, "id") == id {
			entry = mm
		}
	}
	if entry == nil || str(entry, "channel") != "whatsapp" || str(entry, "audience") != "numbers" ||
		num(entry, "phones") != 2 || num(entry, "sent") != 2 || num(entry, "failed") != 0 || num(entry, "queued") != 0 {
		t.Fatalf("history entry = %v", entry)
	}
	if wa, _ := hist["whatsapp"].(map[string]any); wa == nil || wa["enabled"] != true || num(wa, "groupMax") != 20 {
		t.Fatalf("history's WhatsApp settings = %v", hist["whatsapp"])
	}

	// 3. One household: its opted-in number.
	code, hh := ops.call(http.MethodPost, base, map[string]any{"title": "Foyer " + run, "body": "Bonjour.", "channel": "whatsapp",
		"audience": "household", "value": numberB})
	if code != http.StatusCreated || num(hh, "queued") != 1 || fmt.Sprint(hh["recipients"]) != fmt.Sprint([]any{numberB}) {
		t.Fatalf("send to a household = %d %v", code, hh)
	}
	waitForText(t, w.notifier, alertsdomain.Phone(numberB), "Foyer "+run, 10*time.Second)

	// 4. What is refused.
	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("+22170%07d", i)
	}
	for _, path := range []string{base, base + "/preview"} {
		if code, body := ops.call(http.MethodPost, path, map[string]any{"title": "x", "body": "x", "channel": "whatsapp",
			"audience": "numbers", "numbers": many}); code != http.StatusBadRequest {
			t.Fatalf("21 numbers on %s = %d %v, want 400", path, code, body)
		}
	}
	code, none := ops.call(http.MethodPost, base, map[string]any{"title": "x", "body": "x", "channel": "whatsapp",
		"audience": "numbers", "numbers": []string{numberC, numberD}})
	if code != http.StatusConflict || fmt.Sprint(refusedOf(none)) != fmt.Sprint(map[string]string{numberC: "stopped", numberD: "not_opted_in"}) {
		t.Fatalf("a group nobody can receive = %d %v, want 409 with the refused", code, none)
	}
	for _, bad := range []map[string]any{
		{"title": "x", "body": "x", "channel": "whatsapp", "audience": "all"},
		{"title": "x", "body": "x", "channel": "whatsapp", "audience": "tier", "value": "max"},
		{"title": "x", "body": "x", "channel": "whatsapp", "audience": "numbers"},
		{"title": "x", "body": "x", "channel": "sms", "audience": "all"},
		{"title": "x", "body": "x", "audience": "numbers", "numbers": []string{numberA}},
	} {
		if code, body := ops.call(http.MethodPost, base, bad); code != http.StatusBadRequest {
			t.Fatalf("send %v = %d %v, want 400", bad, code, body)
		}
	}

	// 5. The action log.
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM audit_logs WHERE action = 'ANNOUNCEMENT_SENT' AND target_type = 'message' AND target_id = $1`, id)
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM audit_logs WHERE action = 'ANNOUNCEMENT_SENT' AND target_id = $1 AND tenant_id = $2`,
		str(hh, "messageId"), appB.tenantID)

	// 6. A restart: the hub stops with a message still queued; the next
	// hub on the same database sends it.
	w.stopBackground()
	code, queued := ops.call(http.MethodPost, base, map[string]any{"title": "Reprise " + run, "body": "Après redémarrage.",
		"channel": "whatsapp", "audience": "numbers", "numbers": []string{numberA}})
	if code != http.StatusCreated || num(queued, "queued") != 1 {
		t.Fatalf("send while stopped = %d %v", code, queued)
	}
	time.Sleep(5 * whatsAppSendInterval)
	if n := countRows(t, `SELECT count(*) FROM hub_whatsapp_queue WHERE message_id = $1 AND status = 'queued'`, str(queued, "messageId")); n != 1 {
		t.Fatalf("%d rows queued while the hub is stopped, want 1", n)
	}
	for _, s := range w.notifier.Sent() {
		if strings.Contains(s.Text, "Reprise "+run) {
			t.Fatal("a stopped hub sent anyway")
		}
	}
	next := restartHub(t, w)
	waitForText(t, next, alertsdomain.Phone(numberA), "Reprise "+run, 10*time.Second)
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM hub_whatsapp_queue WHERE message_id = $1 AND status = 'sent' AND attempts = 1`, str(queued, "messageId"))
}

// optInAs runs the WhatsApp opt-in for one household's member.
func optInAs(t *testing.T, w *world, o *ownerApp, phone alertsdomain.Phone) {
	t.Helper()
	code, body := o.call("POST", o.tenantPath("whatsapp/optin"), map[string]any{})
	if code != http.StatusCreated {
		t.Fatalf("POST whatsapp/optin = %d %v", code, body)
	}
	link := str(body, "waLink")
	const marker = "text=KERGUI%20"
	idx := strings.Index(link, marker)
	if idx < 0 {
		t.Fatalf("the link pre-fills no code: %q", link)
	}
	if answer := w.send(t, phone, "KERGUI "+link[idx+len(marker):], ""); !strings.Contains(answer, "C'est fait") {
		t.Fatalf("the opt-in was not confirmed: %q", answer)
	}
}

// waitForText waits for a message containing text to reach a number.
func waitForText(t *testing.T, n *console.Notifier, phone alertsdomain.Phone, text string, timeout time.Duration) console.Sent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, s := range n.Sent() {
			if s.To == phone && strings.Contains(s.Text, text) {
				return s
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no message %q reached %s", text, phone.Masked())
	return console.Sent{}
}

// restartHub starts a second hub on the world's database and
// configuration, running its background work, and returns its console
// provider.
func restartHub(t *testing.T, w *world) *console.Notifier {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, err := app.New(ctx, w.cfg, "e2e-restart", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = a.Run(runCtx) }()
	t.Cleanup(func() {
		stop()
		<-done
		a.Close()
	})
	n, ok := a.Notifier().(*console.Notifier)
	if !ok {
		t.Fatalf("the restarted hub is wired with %T", a.Notifier())
	}
	return n
}
