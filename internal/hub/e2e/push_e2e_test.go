package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// Issue #49's acceptance path: a device joins the mock F6600P, the relay
// signs what it saw, and the hub tells the household on both channels — a
// WhatsApp message on the console provider, and a push to the app that
// registered its token, carrying the MAC that opens the device sheet.

const appPushToken = "ExponentPushToken[e2e-owner-phone-0001]"

func TestE2E_NewDevicePushAndWhatsApp(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.household(t, ctx)

	// The owner's app registers its push token, as it does once the alerts
	// are on and it is signed in to the household.
	if code, body := w.owner.call("PUT", w.owner.tenantPath("push-tokens"),
		map[string]any{"token": appPushToken, "platform": "android"}); code != http.StatusNoContent {
		t.Fatalf("PUT push-tokens = %d %v", code, body)
	}

	w.box.Join(mockrouter.Station{
		MAC: intruderMAC, Hostname: "TECNO-POP-10", IP: "192.168.1.77", AP: "DEV.WIFI.AP1",
	})

	alert := w.waitForAlert(t, ownerPhone, 30*time.Second)
	if !strings.Contains(alert.Text, "Un téléphone Tecno") {
		t.Fatalf("the WhatsApp alert does not name the device: %q", alert.Text)
	}
	push := w.waitForPush(t, appPushToken, 30*time.Second)
	if push.Msg.Data["mac"] != intruderMAC {
		t.Fatalf("the push opens %q, want the new device %s", push.Msg.Data["mac"], intruderMAC)
	}
	if push.Msg.Data["tenantId"] != w.owner.tenantID || push.Msg.Data["kind"] != "new_device" {
		t.Errorf("push data = %v", push.Msg.Data)
	}
	if !strings.Contains(push.Msg.Body, "Un téléphone Tecno") || !strings.Contains(push.Msg.Body, "Ouvrez pour l'autoriser ou le bloquer") {
		t.Errorf("the notification does not name the device, or what to do: %q", push.Msg.Body)
	}
	for _, leak := range []string{intruderMAC, "192.168.1.77"} {
		if strings.Contains(push.Msg.Body, leak) || strings.Contains(alert.Text, leak) {
			t.Errorf("a household text shows %s: %q / %q", leak, alert.Text, push.Msg.Body)
		}
	}
	// One alert, one message on each channel.
	pushes := 0
	for _, p := range w.pusher.Pushed() {
		if p.Target.Token == appPushToken {
			pushes++
		}
	}
	if pushes != 1 {
		t.Errorf("%d pushes, want 1", pushes)
	}
	waitForRow(t, 10*time.Second,
		`SELECT count(*) FROM hub_alert_deliveries WHERE tenant_id = $1 AND channel = 'push' AND error = ''`,
		w.owner.tenantID)
}

// waitForPush waits for a notification to reach an app installation.
func (w *world) waitForPush(t *testing.T, token string, timeout time.Duration) console.PushedMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, p := range w.pusher.Pushed() {
			if p.Target.Token == token {
				return p
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("nothing was pushed to the app")
	return console.PushedMessage{}
}
