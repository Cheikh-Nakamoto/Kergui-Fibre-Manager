package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/snapshot"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// These tests exercise H2's second part against a real Postgres: the
// WhatsApp opt-in (the person writes first), and one grouped alert reaching
// the number that opted in.

const testPhone = alertsdomain.Phone("+221771234567")

// consoleNotifier is the provider the test hub is wired with.
func consoleNotifier(t *testing.T, app *App) *console.Notifier {
	t.Helper()
	n, ok := app.Notifier().(*console.Notifier)
	if !ok {
		t.Fatalf("the test hub is wired with %T, want the console provider", app.Notifier())
	}
	return n
}

// optIn runs the whole opt-in: the app asks for a link, the person sends
// the message it pre-fills, and the hub answers in the conversation.
func (h *household) optIn(t *testing.T, srv *httptest.Server, phone alertsdomain.Phone) {
	t.Helper()
	code, body := h.owner.do("POST", h.path("whatsapp/optin"), map[string]any{})
	if code != http.StatusCreated {
		t.Fatalf("POST whatsapp/optin = %d %v", code, body)
	}
	link := str(body, "waLink")
	if !strings.HasPrefix(link, "https://wa.me/221700000000?text=") {
		t.Fatalf("the wa.me link does not point at the Kergui number: %q", link)
	}
	pairing := codeFromLink(t, link)
	if pairing == "" {
		t.Fatalf("the link pre-fills no code: %q", link)
	}
	sendInbound(t, srv, inboundBody{From: string(phone), Text: "KERGUI " + pairing})
}

type inboundBody struct {
	From     string `json:"from"`
	Text     string `json:"text"`
	QuotedID string `json:"quotedId,omitempty"`
	ID       string `json:"id,omitempty"`
}

// sendInbound calls the provider webhook the way the console provider is
// reached: signed with the shared secret, on the secret path.
func sendInbound(t *testing.T, srv *httptest.Server, msg inboundBody) int {
	t.Helper()
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/whatsapp/v1/inbound/"+testWebhookSecret, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(console.SignatureHeader, console.Sign([]byte(testWebhookSecret), raw))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// codeFromLink reads the pairing code out of the wa.me link's text.
func codeFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(u.Query().Get("text"))
	if len(fields) != 2 || fields[0] != "KERGUI" {
		return ""
	}
	return fields[1]
}

// TestAlerts_OptInAndGroupedAlert is H2's second half: a number opts in by
// writing first, and one message announces both devices that arrived.
func TestAlerts_OptInAndGroupedAlert(t *testing.T) {
	srv, app := newRunningTestHub(t, WithAlertWindow(500*time.Millisecond))
	notifier := consoleNotifier(t, app)
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)
	h.optIn(t, srv, testPhone)

	// The confirmation carries the provider's licence notice when the
	// provider requires one; the console one requires none.
	confirmation, ok := notifier.LastTo(testPhone)
	if !ok {
		t.Fatal("the hub did not answer the opt-in message")
	}
	if !strings.Contains(confirmation.Text, "STOP") {
		t.Errorf("the confirmation does not say how to stop: %q", confirmation.Text)
	}

	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10")},
	}); code != http.StatusNoContent {
		t.Fatal("the baseline snapshot was refused")
	}
	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{
			station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10"),
			station("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77"),
			station("bb:22:33:44:55:66", "Chromecast", "192.168.1.78"),
		},
	}); code != http.StatusNoContent {
		t.Fatal("the second snapshot was refused")
	}

	// An alert is told apart from the hub's other messages by the alert it
	// belongs to, not by its wording.
	alert := waitForAlertMessage(t, notifier, testPhone, 30*time.Second)
	if !strings.Contains(alert.Text, "TECNO-POP-10") || !strings.Contains(alert.Text, "Chromecast") {
		t.Fatalf("the alert does not name both arrivals: %q", alert.Text)
	}
	if !strings.Contains(alert.Text, "1.") || !strings.Contains(alert.Text, "2.") {
		t.Errorf("the alert does not number the devices, so a reply cannot pick one: %q", alert.Text)
	}
	if strings.Contains(alert.Text, "aa:11:22:33:44:55") {
		t.Errorf("the alert shows a MAC address to a household that cannot read one: %q", alert.Text)
	}

	// One window, one message: the outbox delivers at least once, and the
	// household hears about it once.
	if n := alertCount(t, h.tenantID); n != 1 {
		t.Errorf("the hub stored %d alerts, want 1", n)
	}
	if got := countAlertMessages(notifier, testPhone); got != 1 {
		t.Errorf("the household received %d alerts, want 1", got)
	}
	// And the delivery is recorded, so the app can show that it went out.
	if n := deliveryCount(t, h.tenantID); n != 1 {
		t.Errorf("%d deliveries recorded, want 1", n)
	}
}

// TestAlerts_StopEndsThem: "STOP" is answered from any number, and no
// further alert reaches it.
func TestAlerts_StopEndsThem(t *testing.T) {
	srv, app := newRunningTestHub(t, WithAlertWindow(500*time.Millisecond))
	notifier := consoleNotifier(t, app)
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)
	h.optIn(t, srv, testPhone)

	if code := sendInbound(t, srv, inboundBody{From: string(testPhone), Text: "STOP"}); code != http.StatusOK {
		t.Fatalf("the STOP webhook answered %d", code)
	}
	stopped, _ := notifier.LastTo(testPhone)
	if !strings.Contains(stopped.Text, "ne recevrez plus") {
		t.Errorf("STOP was not acknowledged: %q", stopped.Text)
	}

	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10")},
	}); code != http.StatusNoContent {
		t.Fatal("the baseline snapshot was refused")
	}
	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{
			station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10"),
			station("aa:11:22:33:44:55", "TECNO-POP-10", "192.168.1.77"),
		},
	}); code != http.StatusNoContent {
		t.Fatal("the second snapshot was refused")
	}

	// The alert is still created — the household's app shows it — but
	// nothing more is sent to the number that stopped.
	waitForAlertRow(t, h.tenantID, 20*time.Second)
	time.Sleep(time.Second)
	if n := countAlertMessages(notifier, testPhone); n != 0 {
		t.Errorf("%d alerts reached a number that asked to stop", n)
	}
}

// TestAlerts_WebhookRefusesTheWrongSecret: the inbound route is reachable
// only with the configured secret, and says nothing to anyone else.
func TestAlerts_WebhookRefusesTheWrongSecret(t *testing.T) {
	srv, app := newRunningTestHub(t)
	notifier := consoleNotifier(t, app)
	h := signupHousehold(t, srv)
	h.optIn(t, srv, testPhone)
	before := len(notifier.Sent())

	raw, err := json.Marshal(inboundBody{From: string(testPhone), Text: "STOP"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/whatsapp/v1/inbound/", "/whatsapp/v1/inbound/wrong-secret"} {
		req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(console.SignatureHeader, console.Sign([]byte(testWebhookSecret), raw))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("POST %s was accepted", path)
		}
	}

	// And a body whose signature does not match, on the right path.
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/whatsapp/v1/inbound/"+testWebhookSecret, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(console.SignatureHeader, "sha256=00")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("an unsigned body was accepted")
	}
	if n := len(notifier.Sent()); n != before {
		t.Errorf("the hub answered %d unauthenticated calls", n-before)
	}
}

// TestAlerts_OptInCodeIsSingleUseAndScoped: a code works once, and only a
// household's owner or admin may issue one.
func TestAlerts_OptInCodeIsSingleUseAndScoped(t *testing.T) {
	srv, app := newRunningTestHub(t)
	notifier := consoleNotifier(t, app)
	h := signupHousehold(t, srv)

	code, body := h.owner.do("POST", h.path("whatsapp/optin"), map[string]any{})
	if code != http.StatusCreated {
		t.Fatalf("POST whatsapp/optin = %d %v", code, body)
	}
	pairing := codeFromLink(t, str(body, "waLink"))

	sendInbound(t, srv, inboundBody{From: string(testPhone), Text: "KERGUI " + pairing})
	if n := optInCount(t, h.tenantID); n != 1 {
		t.Fatalf("%d opt-ins stored, want 1", n)
	}
	// The same code again, from another number: refused, and that number
	// is not linked to the household.
	other := alertsdomain.Phone("+221780000000")
	sendInbound(t, srv, inboundBody{From: string(other), Text: "KERGUI " + pairing})
	if n := optInCount(t, h.tenantID); n != 1 {
		t.Errorf("a spent code linked a second number: %d opt-ins", n)
	}
	refusal, ok := notifier.LastTo(other)
	if !ok || !strings.Contains(refusal.Text, "plus valable") {
		t.Errorf("the second number was not told why: %+v", refusal)
	}

	// Nobody outside the household may route its alerts to their phone.
	stranger := signupHousehold(t, srv)
	if code, _ := stranger.owner.do("POST", h.path("whatsapp/optin"), map[string]any{}); code != http.StatusForbidden {
		t.Errorf("another household's owner got %d from this household's opt-in route, want 403", code)
	}
}

// --- helpers --------------------------------------------------------------

// waitForAlertMessage waits for an alert — a message that carries an alert
// id — to reach a number.
func waitForAlertMessage(t *testing.T, n *console.Notifier, to alertsdomain.Phone, timeout time.Duration) console.Sent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, s := range n.Sent() {
			if s.To == to && s.AlertID != "" {
				return s
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no alert reached %s", to.Masked())
	return console.Sent{}
}

func countAlertMessages(n *console.Notifier, to alertsdomain.Phone) int {
	count := 0
	for _, s := range n.Sent() {
		if s.To == to && s.AlertID != "" {
			count++
		}
	}
	return count
}

func waitForAlertRow(t *testing.T, tenantID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if alertCount(t, tenantID) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("no alert was stored in time")
}

func alertCount(t *testing.T, tenantID string) int {
	return countQuery(t, `SELECT count(*) FROM hub_alerts WHERE tenant_id = $1`, tenantID)
}

func deliveryCount(t *testing.T, tenantID string) int {
	return countQuery(t, `SELECT count(*) FROM hub_alert_deliveries WHERE tenant_id = $1 AND channel = 'whatsapp'`, tenantID)
}

func optInCount(t *testing.T, tenantID string) int {
	return countQuery(t, `SELECT count(*) FROM hub_whatsapp_optins WHERE tenant_id = $1 AND stopped_at IS NULL`, tenantID)
}

func countQuery(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool(t).QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAudit_HubActionsReachTheAuditLog is a regression test for three
// defects found in H1's audit trail, all of which made "who did what to the
// box" unrecordable:
//
//   - audit_action is a Postgres enum owned by the kernel, and none of the
//     hub's own actions were in it, so every row was refused and retried
//     ten times — which also starved the dispatcher that carries alerts;
//   - audit_logs.actor_id is a foreign key to users, and the relay path
//     passed an agent id;
//   - audit_logs.target_id is a uuid, and the block path passed a MAC.
func TestAudit_HubActionsReachTheAuditLog(t *testing.T) {
	srv, _ := newRunningTestHub(t)
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)
	h.optIn(t, srv, testPhone)

	// A relay that answers a command: an actor that is not a user.
	if code, _ := r.pushObservations(snapshot.Observations{
		Hosts: []port.ObservationView{station("ac:bb:cc:00:00:11", "Phone-Salon", "192.168.1.10")},
	}); code != http.StatusNoContent {
		t.Fatal("the baseline snapshot was refused")
	}
	go func() { r.serveOnce(2*time.Second, http.StatusOK, `{"blocked":true}`) }()
	if code, body := h.owner.do("POST", h.path("router/devices/ac:bb:cc:00:00:22/block"),
		h.blockCommand("ac:bb:cc:00:00:22")); code != http.StatusOK && code != http.StatusAccepted {
		t.Fatalf("POST block = %d %v", code, body)
	}

	// Every one of these is a hub action, so every one of them exercises
	// the enum the kernel owns.
	for _, action := range []string{
		"PAIRING_CODE_ISSUED", "AGENT_PAIRED", "NETWORK_UPDATED", "TRUST_BUNDLE_UPDATED",
		"WHATSAPP_INVITED", "WHATSAPP_LINKED", "ROUTER_COMMAND_QUEUED", "ROUTER_COMMAND_DONE",
	} {
		waitForAuditRow(t, action, 20*time.Second)
	}
	// And nothing in the outbox is stuck failing.
	if n := countQuery(t, `SELECT count(*) FROM event_outbox WHERE last_error IS NOT NULL`); n != 0 {
		t.Errorf("%d outbox events are failing; the first error: %s", n, firstOutboxError(t))
	}

	// The MAC of a blocked device is still recorded, in the metadata.
	if n := countQuery(t,
		`SELECT count(*) FROM audit_logs
		 WHERE action = 'ROUTER_COMMAND_QUEUED' AND metadata->>'mac' = $1`,
		"ac:bb:cc:00:00:22"); n == 0 {
		t.Error("the audit trail does not say which device was blocked")
	}
}

func waitForAuditRow(t *testing.T, action string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countQuery(t, `SELECT count(*) FROM audit_logs WHERE action = $1`, action) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("no %s row ever reached audit_logs", action)
}

func firstOutboxError(t *testing.T) string {
	t.Helper()
	var err string
	_ = testPool(t).QueryRow(context.Background(),
		`SELECT left(last_error, 200) FROM event_outbox WHERE last_error IS NOT NULL LIMIT 1`).Scan(&err)
	return err
}
