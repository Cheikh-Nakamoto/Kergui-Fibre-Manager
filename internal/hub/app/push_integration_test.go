package app

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/adapters/console"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/snapshot"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// App push (issue #49) against a real Postgres: the app registers its token
// on PUT push-tokens, a new device is pushed to it with its MAC, and DELETE
// push-tokens/{token} stops it.

const testPushToken = "ExponentPushToken[integration-test-0001]"

func consolePusher(t *testing.T, app *App) *console.Pusher {
	t.Helper()
	p, ok := app.Pusher().(*console.Pusher)
	if !ok {
		t.Fatalf("the test hub pushes through %T, want the console channel", app.Pusher())
	}
	return p
}

func TestPush_TokenRegistrationAndAlert(t *testing.T) {
	srv, app := newRunningTestHub(t, WithAlertWindow(500*time.Millisecond))
	pusher := consolePusher(t, app)
	h := signupHousehold(t, srv)
	r := h.activeRelay(t)

	for name, body := range map[string]map[string]any{
		"no token":         {"platform": "android"},
		"unknown platform": {"token": testPushToken, "platform": "web"},
		"spaces":           {"token": "Expo Push Token", "platform": "android"},
		"unknown field":    {"token": testPushToken, "platform": "android", "userId": "someone-else"},
	} {
		if code, b := h.owner.do("PUT", h.path("push-tokens"), body); code != http.StatusBadRequest {
			t.Errorf("%s: PUT push-tokens = %d %v, want 400", name, code, b)
		}
	}
	if code, b := h.owner.do("PUT", h.path("push-tokens"), map[string]any{"token": testPushToken, "platform": "android"}); code != http.StatusNoContent {
		t.Fatalf("PUT push-tokens = %d %v", code, b)
	}
	// Registering again is harmless: one installation, one row.
	if code, _ := h.owner.do("PUT", h.path("push-tokens"), map[string]any{"token": testPushToken, "platform": "android"}); code != http.StatusNoContent {
		t.Fatal("registering the same token again was refused")
	}
	if n := countQuery(t, `SELECT count(*) FROM hub_push_tokens WHERE tenant_id = $1`, h.tenantID); n != 1 {
		t.Fatalf("%d tokens stored, want 1", n)
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

	got := waitForPush(t, pusher, testPushToken, 30*time.Second)
	if got.Msg.Data["mac"] != "aa:11:22:33:44:55" || got.Msg.Data["tenantId"] != h.tenantID || got.Msg.Data["alertId"] == "" {
		t.Fatalf("the push cannot open the device sheet: %v", got.Msg.Data)
	}
	if got.Target.Platform != "android" || got.Target.UserID != h.userID {
		t.Errorf("pushed to %+v", got.Target)
	}
	waitForRowCount(t, `SELECT count(*) FROM hub_alert_deliveries WHERE tenant_id = $1 AND channel = 'push'`, h.tenantID, 1)

	if code, b := h.owner.do("DELETE", h.path("push-tokens/"+url.PathEscape(testPushToken)), nil); code != http.StatusNoContent {
		t.Fatalf("DELETE push-tokens = %d %v", code, b)
	}
	targets, err := app.alerts.Repo.For(context.Background(), h.tenantID)
	if err != nil || len(targets) != 0 {
		t.Fatalf("after DELETE: %v %v", targets, err)
	}
}

func waitForPush(t *testing.T, p *console.Pusher, token string, timeout time.Duration) console.PushedMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, m := range p.Pushed() {
			if m.Target.Token == token {
				return m
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("nothing was pushed to the app")
	return console.PushedMessage{}
}

func waitForRowCount(t *testing.T, query, tenantID string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if countQuery(t, query, tenantID) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: never %d", query, want)
}
