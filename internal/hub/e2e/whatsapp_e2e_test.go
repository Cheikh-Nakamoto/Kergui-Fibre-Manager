package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// This is H2's acceptance test, with nothing simulated between the box and
// the block: a device joins the mock F6600P, the relay signs what it saw,
// the hub finds it new, an alert goes out on the console provider, the
// household answers "1", and the device is blocked on the mock.

const (
	ownerPhone = alertsdomain.Phone("+221771234567")
	// intruderMAC is the device that joins during the test.
	intruderMAC = "aa:11:22:33:44:55"
	// protectedMAC is a device of the captured fixture that the owner pins.
	// It is one the fixture does not already have in the box's MAC filter,
	// so "is it blocked?" means something here.
	protectedMAC = "ac:bb:cc:00:00:33"
	// relayMAC is the address the PC relay answers to on the mock box. The
	// owner's app copies it into the bundle, which is what protects a relay
	// that is not the one holding the box.
	relayMAC = "aa:bb:cc:dd:ee:50"
)

// household sets the bench up to the point where an alert can be answered:
// a trusted relay, the hub's narrow key delegated, one device pinned, and a
// number opted in.
func (w *world) household(t *testing.T, ctx context.Context) hubKeyInfo {
	t.Helper()
	key := w.owner.hubKey()
	if !key.Enabled {
		t.Fatal("the bench has no hub key seed, so there is nothing to test")
	}
	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)

	// The relay is itself on the Wi-Fi, as a PC left on at home would be.
	w.box.Join(mockrouter.Station{
		MAC: relayMAC, Hostname: "PC-du-relais", IP: "192.168.1.50", AP: "DEV.WIFI.AP1",
	})
	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")

	relay := trustRelay(state, "pc", t)
	relay.MACs = []string{relayMAC}
	w.owner.publishBundleWith(w.boxPin,
		[]trust.Relay{relay},
		[]trust.Delegation{key.delegation(t, time.Now().Add(30*24*time.Hour))},
		[]string{protectedMAC})
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)
	w.optIn(t, ownerPhone)
	waitForInventory(t, w, 4)
	return key
}

// TestE2E_AlertThenBlockByReply is the acceptance criterion of H2.
func TestE2E_AlertThenBlockByReply(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.household(t, ctx)

	// A device nobody has seen joins the Wi-Fi.
	w.box.Join(mockrouter.Station{
		MAC: intruderMAC, Hostname: "TECNO-POP-10", IP: "192.168.1.77", AP: "DEV.WIFI.AP1",
	})

	alert := w.waitForAlert(t, ownerPhone, 30*time.Second)
	if !strings.Contains(alert.Text, "TECNO-POP-10") {
		t.Fatalf("the alert does not name the device: %q", alert.Text)
	}
	if !strings.Contains(alert.Text, "Répondez 1") {
		t.Errorf("the alert does not tell the household what to answer: %q", alert.Text)
	}

	// "1", answered in the conversation the alert arrived in.
	answer := w.reply(t, ownerPhone, "1", alert.MessageID)
	if !w.box.Blocked(intruderMAC) {
		t.Fatalf("the device is not blocked on the box; the hub answered: %q", answer)
	}
	if !strings.Contains(answer, "✅") {
		t.Errorf("the household was not told it worked: %q", answer)
	}

	// Answering again blocks nothing more and says so, which is what makes
	// a provider's retry harmless.
	again := w.reply(t, ownerPhone, "1", alert.MessageID)
	if !strings.Contains(again, "déjà") {
		t.Errorf("a repeated reply was not recognised: %q", again)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_reply_actions WHERE tenant_id = $1`, w.owner.tenantID); n != 1 {
		t.Errorf("%d reply actions recorded, want 1", n)
	}

	// The audit trail names who asked, through which channel, for which
	// device. It travels through the outbox, so it is waited for.
	waitForRow(t, 20*time.Second,
		`SELECT count(*) FROM audit_logs
		 WHERE tenant_id = $1 AND action = 'WHATSAPP_BLOCK_REQUESTED'
		   AND metadata->>'channel' = 'whatsapp' AND metadata->>'mac' = $2`,
		w.owner.tenantID, intruderMAC)
}

// TestE2E_ReplyRefusals gathers what a reply may not do. The hub holds this
// key, so these are the things an attacker who owned the hub would try.
func TestE2E_ReplyRefusals(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.household(t, ctx)

	w.box.Join(mockrouter.Station{
		MAC: intruderMAC, Hostname: "TECNO-POP-10", IP: "192.168.1.77", AP: "DEV.WIFI.AP1",
	})
	alert := w.waitForAlert(t, ownerPhone, 30*time.Second)
	alertID := alertIDFor(t, w)

	t.Run("a number nobody registered", func(t *testing.T) {
		answer := w.reply(t, "+221780000001", "1", alert.MessageID)
		if w.box.Blocked(intruderMAC) {
			t.Fatal("a stranger's reply blocked a device")
		}
		// A stranger is told there is nothing to answer, which is also all
		// they get to learn: no household, no device, no alert.
		if !strings.Contains(answer, "pas d'alerte") && !strings.Contains(answer, "pas autorisé") {
			t.Errorf("the stranger was not refused clearly: %q", answer)
		}
		for _, leak := range []string{intruderMAC, "TECNO", w.owner.tenantID} {
			if strings.Contains(answer, leak) {
				t.Errorf("the refusal tells a stranger about %q: %q", leak, answer)
			}
		}
	})

	t.Run("a device the alert never showed", func(t *testing.T) {
		answer := w.reply(t, ownerPhone, "7", alert.MessageID)
		if !strings.Contains(answer, "ne correspond") {
			t.Errorf("answer = %q, want a refusal naming no device", answer)
		}
		if w.box.Blocked(protectedMAC) || w.box.Blocked(intruderMAC) {
			t.Error("a number the alert never showed reached a device")
		}
	})

	t.Run("an unsigned inbound call", func(t *testing.T) {
		body, err := json.Marshal(map[string]any{"from": string(ownerPhone), "text": "1"})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost,
			w.hub.URL+"/whatsapp/v1/inbound/"+webhookSecret, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		// No signature header at all.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Error("an unsigned inbound call was accepted")
		}
		if w.box.Blocked(intruderMAC) {
			t.Error("an unsigned call reached the box")
		}
	})

	// The next three put a device into the household's own alert by writing
	// straight to the hub's database: the worst case, a hub forging the
	// alert it then acts on.
	t.Run("a device the owner pinned", func(t *testing.T) {
		forgeAlertDevice(t, w, alertID, 9, protectedMAC, "Téléphone de la maison")
		answer := w.reply(t, ownerPhone, "9", alert.MessageID)
		if w.box.Blocked(protectedMAC) {
			t.Fatal("the narrow key blocked a protected device")
		}
		if !strings.Contains(answer, "protégé") {
			t.Errorf("answer = %q, want the device named as protected", answer)
		}
	})

	t.Run("the household's own relay", func(t *testing.T) {
		forgeAlertDevice(t, w, alertID, 8, relayMAC, "PC-du-relais")
		answer := w.reply(t, ownerPhone, "8", alert.MessageID)
		if w.box.Blocked(relayMAC) {
			t.Fatal("the narrow key cut the household's own relay")
		}
		if !strings.Contains(answer, "protégé") {
			t.Errorf("answer = %q, want the relay named as protected", answer)
		}
	})

	t.Run("a device that is not on the network", func(t *testing.T) {
		// The relay's own last word: an address the box never reported is
		// refused where it matters, whatever the hub says about it.
		forgeAlertDevice(t, w, alertID, 6, "de:ad:be:ef:00:01", "Appareil fantôme")
		answer := w.reply(t, ownerPhone, "6", alert.MessageID)
		if !strings.Contains(answer, "connecté") && !strings.Contains(answer, "refusé") {
			t.Errorf("answer = %q, want a refusal", answer)
		}
		if w.box.Blocked("de:ad:be:ef:00:01") {
			t.Error("a device the box never reported was blocked")
		}
	})
}

// TestE2E_ReplyIsRateLimited: the narrow key is capped, so a hub that ran
// amok could cut a few devices at most before being told to stop.
func TestE2E_ReplyIsRateLimited(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.household(t, ctx)

	macs := []string{
		"aa:00:00:00:00:01", "aa:00:00:00:00:02", "aa:00:00:00:00:03",
		"aa:00:00:00:00:04", "aa:00:00:00:00:05", "aa:00:00:00:00:06",
	}
	for i, mac := range macs {
		w.box.Join(mockrouter.Station{
			MAC: mac, Hostname: fmt.Sprintf("Appareil-%d", i+1),
			IP: fmt.Sprintf("192.168.1.8%d", i), AP: "DEV.WIFI.AP1",
		})
	}
	alert := w.waitForAlert(t, ownerPhone, 30*time.Second)

	// Answer every number the alert showed, in one message.
	numbers := make([]string, 0, len(macs))
	for i := 1; i <= alertDeviceCount(t, w); i++ {
		numbers = append(numbers, fmt.Sprint(i))
	}
	answer := w.reply(t, ownerPhone, strings.Join(numbers, " "), alert.MessageID)

	blocked := 0
	for _, mac := range macs {
		if w.box.Blocked(mac) {
			blocked++
		}
	}
	if blocked > trust.DefaultHubBlockLimit {
		t.Fatalf("%d devices were blocked by one message, the limit is %d", blocked, trust.DefaultHubBlockLimit)
	}
	if len(numbers) > trust.DefaultHubBlockLimit && !strings.Contains(strings.ToLower(answer), "trop") {
		t.Errorf("the household was not told about the limit: %q", answer)
	}
}

// --- the household's phone -----------------------------------------------

// hubKeyInfo is what the app reads in order to delegate the narrow key.
type hubKeyInfo struct {
	Enabled bool
	KeyID   string
	Keys    trust.PublicKeys
}

// delegation is how the owner grants that key its one power.
func (k hubKeyInfo) delegation(t *testing.T, expires time.Time) trust.Delegation {
	t.Helper()
	if !k.Enabled {
		t.Fatal("the hub key is not enabled")
	}
	return trust.Delegation{Keys: k.Keys, Scope: trust.ScopeHubBlock, ExpiresAt: expires, Label: "WhatsApp"}
}

// hubKey reads the household's hub key from the hub.
func (o *ownerApp) hubKey() hubKeyInfo {
	o.t.Helper()
	code, body := o.call("GET", o.tenantPath("hub-key"), nil)
	if code != http.StatusOK {
		o.t.Fatalf("GET hub-key = %d %v", code, body)
	}
	enabled, _ := body["enabled"].(bool)
	return hubKeyInfo{
		Enabled: enabled, KeyID: str(body, "keyId"),
		Keys: trust.PublicKeys{
			Ed25519: decodeB64(o.t, body["ed25519"]),
			X25519:  decodeB64(o.t, body["x25519"]),
		},
	}
}

// optIn runs the WhatsApp opt-in: the app asks for a link, the person sends
// the message it pre-fills, and the hub answers in the conversation.
func (w *world) optIn(t *testing.T, phone alertsdomain.Phone) {
	t.Helper()
	code, body := w.owner.call("POST", w.owner.tenantPath("whatsapp/optin"), map[string]any{})
	if code != http.StatusCreated {
		t.Fatalf("POST whatsapp/optin = %d %v", code, body)
	}
	link := str(body, "waLink")
	const marker = "text=KERGUI%20"
	idx := strings.Index(link, marker)
	if idx < 0 {
		t.Fatalf("the link pre-fills no code: %q", link)
	}
	answer := w.send(t, phone, "KERGUI "+link[idx+len(marker):], "")
	if !strings.Contains(answer, "C'est fait") {
		t.Fatalf("the opt-in was not confirmed: %q", answer)
	}
}

// waitForAlert waits for an alert — a message carrying an alert id — to
// reach a number.
func (w *world) waitForAlert(t *testing.T, phone alertsdomain.Phone, timeout time.Duration) console.Sent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, s := range w.notifier.Sent() {
			if s.To == phone && s.AlertID != "" {
				return s
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no alert reached %s", phone.Masked())
	return console.Sent{}
}

// reply answers an alert and returns what the hub said back.
func (w *world) reply(t *testing.T, phone alertsdomain.Phone, text, quotedID string) string {
	t.Helper()
	return w.send(t, phone, text, quotedID)
}

// send delivers one inbound message the way the console provider is
// reached — signed, on the secret path — and returns the hub's answer.
func (w *world) send(t *testing.T, phone alertsdomain.Phone, text, quotedID string) string {
	t.Helper()
	before := len(w.notifier.Sent())
	body, err := json.Marshal(map[string]any{
		"from": string(phone), "text": text, "quotedId": quotedID,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost,
		w.hub.URL+"/whatsapp/v1/inbound/"+webhookSecret, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(console.SignatureHeader, console.Sign([]byte(webhookSecret), body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the inbound webhook answered %d", resp.StatusCode)
	}
	sent := w.notifier.Sent()
	if len(sent) <= before {
		t.Fatalf("the hub did not answer %q", text)
	}
	return sent[len(sent)-1].Text
}

func decodeB64(t *testing.T, v any) []byte {
	t.Helper()
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	// Go encodes []byte as base64 in JSON.
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding key material: %v", err)
	}
	return raw
}

// --- the hub's database --------------------------------------------------

// waitForRow waits for a counting query to return at least one row's worth,
// for the facts that travel through the outbox.
func waitForRow(t *testing.T, timeout time.Duration, query string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countRows(t, query, args...) > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("no row ever matched: %s", query)
}

func alertIDFor(t *testing.T, w *world) string {
	t.Helper()
	var id string
	if err := e2ePool(t).QueryRow(context.Background(),
		`SELECT id FROM hub_alerts WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT 1`,
		w.owner.tenantID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func alertDeviceCount(t *testing.T, w *world) int {
	t.Helper()
	return countRows(t,
		`SELECT coalesce(max(jsonb_array_length(devices)), 0) FROM hub_alerts WHERE tenant_id = $1`,
		w.owner.tenantID)
}

// forgeAlertDevice writes a device into an alert without going through the
// inventory, which no API allows. It is how the test plays the worst case:
// a hub that made up the alert it is about to act on.
func forgeAlertDevice(t *testing.T, w *world, alertID string, no int, mac, hostname string) {
	t.Helper()
	item, err := json.Marshal(map[string]any{
		"no": no, "mac": mac, "device_id": "DEVICE-FORGED", "hostname": hostname,
	})
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, `UPDATE hub_alerts SET devices = devices || $2::jsonb WHERE id = $1`, alertID, item)
}
