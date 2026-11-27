package mobilecore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// RFC-007 D6 (#102) in the phone's core: « liste pleine » reaches the app as
// a code with its numbers, and the stale rules go once a day.

// TestCoreBlockListFull: the box's list is full and nothing is stale: 409
// access_list_full, kind and numbers for the app's sentence.
func TestCoreBlockListFull(t *testing.T) {
	c, _, box := gatedCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	mac := aDevice(t, c)
	used := len(box.FirewallRules())
	box.SetRuleLimits(used, 0)

	status, body := call(t, c, "POST", "/api/devices/"+mac+"/block", "")
	if status != http.StatusConflict {
		t.Fatalf("block = %d %s, want 409", status, body)
	}
	var got struct {
		Code  string `json:"code"`
		Kind  string `json:"kind"`
		Limit int    `json:"limit"`
		Used  int    `json:"used"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "access_list_full" || got.Kind != "firewall" || got.Limit != used || got.Used != used {
		t.Fatalf("body = %s, want access_list_full firewall %d/%d", body, used, used)
	}
	if journalHas(t, c, "La liste de la box est pleine") == 0 {
		t.Error("the refusal is not in the journal")
	}
}

// TestCorePurgesStaleRulesDaily: a device that left the box keeps the rule
// kergui wrote for it until it has been away 30 days; the next device list
// after that removes it, with a journal line in plain French.
func TestCorePurgesStaleRulesDaily(t *testing.T) {
	c, clk, box := autoRejectCore(t)
	const gone = "02:aa:bb:00:00:61"
	box.Join(mockrouter.Station{MAC: gone, Hostname: "Vieux-Telephone", IP: "192.168.1.61"})
	if _, ok := devices(t, c)[gone]; !ok {
		t.Fatalf("%s not listed", gone)
	}
	box.Forget(gone)
	box.AddFirewallRule(gone, gone)

	// The inventory is dated by the real clock, the purge by the core's.
	clk.set(time.Now().Add(29 * gateDay))
	devices(t, c)
	if !box.Blocked(gone) {
		t.Fatal("the rule went before 30 days")
	}
	clk.set(time.Now().Add(31 * gateDay))
	devices(t, c)
	if box.Blocked(gone) {
		t.Fatal("the rule of a device away 31 days is still on the box")
	}
	if n := journalHas(t, c, "Règle retirée : appareil absent depuis 30 jours (Vieux-Telephone)"); n != 1 {
		t.Fatalf("%d journal lines for the removal, want 1", n)
	}
}

// TestDemoListFull: the demo fills the box's list on request, so the store
// reviewer and the Maestro flow see « liste pleine »; the absent blocked
// device is listed; the demo goes back to normal after.
func TestDemoListFull(t *testing.T) {
	c := demoCore(t, nil)
	const mac = "ac:bb:cc:00:00:22" // Laptop-Bureau, not blocked
	if status, body := call(t, c, "POST", "/api/demo/list-full", `{"full":true}`); status != http.StatusOK {
		t.Fatalf("list-full = %d %s", status, body)
	}
	status, body := call(t, c, "POST", "/api/devices/"+mac+"/block", "")
	if status != http.StatusConflict || !strings.Contains(body, `"code": "access_list_full"`) {
		t.Fatalf("block on a full demo box = %d %s", status, body)
	}
	status, body = call(t, c, "GET", "/api/blocked", "")
	if status != http.StatusOK || !strings.Contains(body, `"present": false`) {
		t.Fatalf("GET /api/blocked = %d %s, want an absent blocked device", status, body)
	}
	// An address the Wi-Fi refuses and the app never saw (#103): the purge
	// keeps it, the household frees it by hand.
	if refused := blockedEntry(t, body, DemoRefusedMAC); refused == nil || !refused.WiFi || refused.LastSeen != "" {
		t.Fatalf("GET /api/blocked = %s, want %s refused on the Wi-Fi and unknown", body, DemoRefusedMAC)
	}
	if status, body := call(t, c, "POST", "/api/devices/"+DemoRefusedMAC+"/wifi-unblock", ""); status != http.StatusOK {
		t.Fatalf("wifi-unblock %s = %d %s", DemoRefusedMAC, status, body)
	}
	if _, body := call(t, c, "GET", "/api/blocked", ""); blockedEntry(t, body, DemoRefusedMAC) != nil {
		t.Fatalf("%s still listed after the removal by hand: %s", DemoRefusedMAC, body)
	}
	// Filling it again brings the refusal back, once.
	call(t, c, "POST", "/api/demo/list-full", `{"full":true}`)
	call(t, c, "POST", "/api/demo/list-full", `{"full":true}`)
	if _, body := call(t, c, "GET", "/api/blocked", ""); blockedEntry(t, body, DemoRefusedMAC) == nil {
		t.Fatalf("%s not refused again: %s", DemoRefusedMAC, body)
	}
	if status, body := call(t, c, "POST", "/api/demo/list-full", `{"full":false}`); status != http.StatusOK {
		t.Fatalf("list-full off = %d %s", status, body)
	}
	if status, body := call(t, c, "POST", "/api/devices/"+mac+"/block", ""); status != http.StatusOK {
		t.Fatalf("block after = %d %s", status, body)
	}
}

type blockedRow struct {
	MAC      string `json:"mac"`
	LastSeen string `json:"last_seen"`
	Firewall bool   `json:"firewall"`
	WiFi     bool   `json:"wifi"`
}

func blockedEntry(t *testing.T, body, mac string) *blockedRow {
	t.Helper()
	var got struct {
		Devices []blockedRow `json:"devices"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("GET /api/blocked: %v (%s)", err, body)
	}
	for _, d := range got.Devices {
		if d.MAC == mac {
			return &d
		}
	}
	return nil
}

func TestDemoListFullOnlyInTheDemo(t *testing.T) {
	c := newCore(t, nil)
	if err := c.Configure("http://192.168.1.1", "", ""); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(t, c, "POST", "/api/demo/list-full", `{"full":true}`); status != http.StatusNotFound {
		t.Fatalf("list-full on a real box = %d, want 404", status)
	}
}
