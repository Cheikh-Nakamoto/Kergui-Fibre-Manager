package mobilecore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// The owner's phones (#87, parent #69): the app hands the core the MACs the
// household's hub lists as the owner's (PUT /api/owner-devices), as it hands
// it the approvals; the core marks those devices "Owner", approved and never
// new, on the box's list and on the relay's list in remote mode.

func setOwnerDevices(t *testing.T, c *Core, body string) {
	t.Helper()
	if status, out := call(t, c, "PUT", "/api/owner-devices", body); status != http.StatusOK {
		t.Fatalf("PUT /api/owner-devices = %d %s", status, out)
	}
}

func TestOwnerDeviceIsMarkedOwnerApprovedNeverAnArrival(t *testing.T) {
	c, _, box := gatedCore(t)
	const ownerPhone, stranger = "fa:00:00:00:00:87", "02:00:00:00:00:98"
	markedList(t, c) // the first listing: the baseline
	box.Join(mockrouter.Station{MAC: ownerPhone, Hostname: "Tel-Papa", IP: "192.168.1.87"})
	box.Join(mockrouter.Station{MAC: stranger, Hostname: "Inconnu", IP: "192.168.1.98"})

	// Before the list is handed over, the owner's phone is a device like
	// any other.
	if d := markedList(t, c)[ownerPhone]; d.Mark != "" || d.Approved || !d.NewlySeen {
		t.Fatalf("the owner's phone before the list = %+v, want unmarked and new", d)
	}

	setOwnerDevices(t, c, `{"macs":["FA-00-00-00-00-87","not a mac"]}`)
	list := markedList(t, c)
	if d := list[ownerPhone]; d.Mark != MarkOwner || !d.Approved || d.NewlySeen {
		t.Fatalf("the owner's phone = %+v, want marked Owner, approved, not new", d)
	}
	if d := list[stranger]; d.Mark != "" || d.Approved {
		t.Fatalf("a device that joined = %+v, want unmarked, not approved", d)
	}

	// This phone is the owner's: it is « Ce téléphone » first.
	c.self.set(func(string) domain.IP { return domain.MustIP("192.168.1.87") })
	if d := markedList(t, c)[ownerPhone]; d.Mark != MarkSelf {
		t.Fatalf("the owner's phone seen from itself = %+v, want Self", d)
	}
	c.self.set(func(string) domain.IP { return domain.IP{} })

	// A new list replaces the old one (a recovery: another phone is the
	// owner's).
	setOwnerDevices(t, c, `{"macs":[]}`)
	if d := markedList(t, c)[ownerPhone]; d.Mark != "" {
		t.Fatalf("after the list was emptied = %+v, want unmarked", d)
	}
}

// Auto-reject spares the owner's phone like this phone: handed over, it is
// accepted, and never refused at the delay.
func TestOwnerDeviceIsSparedByAutoReject(t *testing.T) {
	c, clk, box := autoRejectCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0))
	if status, body := setAutoReject(t, c, true); status != http.StatusOK {
		t.Fatalf("turn on with Max = %d %s", status, body)
	}
	const ownerPhone = "fa:00:00:00:00:88"
	box.Join(mockrouter.Station{MAC: ownerPhone, IP: "192.168.1.88"})
	runAutoReject(t, c)
	setOwnerDevices(t, c, `{"macs":["`+ownerPhone+`"]}`)
	clk.set(gateT0.Add(16 * time.Minute))
	if run := runAutoReject(t, c); len(run.Refused) != 0 || box.WiFiBlocked(ownerPhone) {
		t.Fatalf("the owner's phone was refused: %+v", run)
	}
}

// Remote mode: the relay's list carries the Owner mark for the household's
// owner phones, and no relay's own « Self ».
func TestOpenDeviceSnapshot_MarksTheOwnersPhones(t *testing.T) {
	c, _, _ := gatedCore(t) // the core's API runs once it was pointed at a box
	relay := newRelay(t)
	bundle := bundleWith(t, c, credTenant, relay)
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	list := `[{"MAC":"FA:00:00:00:00:87","Hostname":"Tel-Papa"},` +
		`{"MAC":"ac:bb:cc:00:00:22","Hostname":"Laptop-Bureau"},` +
		`{"MAC":"fa:00:00:00:00:99","Hostname":"Relais","Mark":"Self"}]`
	setOwnerDevices(t, c, `{"macs":["fa:00:00:00:00:87"],"tenantId":"`+credTenant+`"}`)

	raw, err := c.OpenDeviceSnapshot(credTenant, bundle, signedSnapshot(t, relay, credTenant, "devices", agentName(0), list, at))
	if err != nil {
		t.Fatalf("OpenDeviceSnapshot: %v", err)
	}
	var got struct {
		Devices []markedDevice `json:"devices"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	marks := map[string]string{}
	for _, d := range got.Devices {
		marks[strings.ToLower(d.MAC)] = d.Mark
	}
	want := map[string]string{"fa:00:00:00:00:87": MarkOwner, "ac:bb:cc:00:00:22": "", "fa:00:00:00:00:99": ""}
	for mac, mark := range want {
		if m, ok := marks[mac]; !ok || m != mark {
			t.Fatalf("%s marked %q (listed %v), want %q: %s", mac, m, ok, mark, raw)
		}
	}
	// The box's own list is not the household's remote one.
	if status, body := call(t, c, "GET", "/api/owner-devices", ""); status != http.StatusOK || strings.Contains(body, "fa:00:00:00:00:87") {
		t.Fatalf("the local list = %d %s, want empty", status, body)
	}
}
