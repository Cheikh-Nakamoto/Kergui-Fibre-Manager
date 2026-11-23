package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// TestE2E_NonApprovedDeviceAlertsOnEveryArrival is issue #58 end to end: a
// device nobody approved alerts when it joins, again when it leaves and
// comes back, and never again once the household approved it.
func TestE2E_NonApprovedDeviceAlertsOnEveryArrival(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin)
	_, state, _ := w.startRelay(ctx, w.owner.invite("pc"), "pc", "192.168.1.50")
	w.owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	w.owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)
	waitForInventory(t, w, 3)

	const guest = "aa:11:22:33:44:55"
	visitor := mockrouter.Station{MAC: guest, Hostname: "Invite-Tel", IP: "192.168.1.77", AP: "DEV.WIFI.AP1"}

	// First arrival.
	w.box.Join(visitor)
	waitForEventCount(t, w.owner.tenantID, 1)

	// It leaves (the box keeps its lease, unassociated), then comes back.
	w.box.Leave(guest)
	waitForAssociated(t, w, guest, false)
	w.box.Join(visitor)
	waitForEventCount(t, w.owner.tenantID, 2)

	// The household approves it: « Approuver » in the app, the same notion
	// as a « 2 » on WhatsApp.
	if status, body := w.owner.call(http.MethodPut, w.owner.tenantPath("router/approvals/"+guest), nil); status != http.StatusOK {
		t.Fatalf("approve: %d %v", status, body)
	}
	if status, body := w.owner.call(http.MethodGet, w.owner.tenantPath("router/inventory"), nil); status != http.StatusOK {
		t.Fatalf("inventory: %d %v", status, body)
	} else if approved, _ := body["approved"].([]any); len(approved) != 1 {
		t.Fatalf("the inventory lists %d approvals, want the guest's: %v", len(approved), body["approved"])
	}

	w.box.Leave(guest)
	waitForAssociated(t, w, guest, false)
	w.box.Join(visitor)
	waitForAssociated(t, w, guest, true)
	// Give the sweep more than one window to be sure nothing was opened.
	time.Sleep(2 * time.Second)
	if n := len(currentEvents(t, w.owner.tenantID)); n != 2 {
		t.Fatalf("got %d alerts, want the 2 from before the approval", n)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_inventory_windows WHERE tenant_id = $1`, w.owner.tenantID); n != 2 {
		t.Fatalf("got %d alert windows, want 2", n)
	}
}

// waitForEventCount waits until the household has exactly n alerts.
func waitForEventCount(t *testing.T, tenantID string, n int) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		got := len(currentEvents(t, tenantID))
		if got > n {
			t.Fatalf("got %d alerts, want %d", got, n)
		}
		if got == n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the household never got its alert number %d (has %d)", n, len(currentEvents(t, tenantID)))
}

// waitForAssociated waits for a snapshot that shows the MAC on the Wi-Fi, or
// not, and for the hub's inventory to have absorbed it.
func waitForAssociated(t *testing.T, w *world, mac string, want bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, h := range storedHosts(t, w) {
			if h.MAC == mac && h.Associated == want &&
				countRows(t, `SELECT count(*) FROM hub_inventory_macs WHERE tenant_id = $1 AND mac = $2 AND present = $3`,
					w.owner.tenantID, mac, want) == 1 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s never showed up with associated=%v", mac, want)
}
