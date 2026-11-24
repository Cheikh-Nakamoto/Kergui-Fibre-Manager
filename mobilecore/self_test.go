package mobilecore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// This phone (#86, parent #69): the device of each listing that is the phone
// running the app — its address on the box, as the self locator finds it,
// and its MAC once learnt from that match — comes back marked "Self",
// approved, and never new.

type markedDevice struct {
	MAC       string
	IP        string
	Mark      string
	Approved  bool
	NewlySeen bool
}

func markedList(t *testing.T, c *Core) map[string]markedDevice {
	t.Helper()
	status, body := call(t, c, "GET", "/api/devices", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/devices = %d %s", status, body)
	}
	var list []markedDevice
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	out := map[string]markedDevice{}
	for _, d := range list {
		out[strings.ToLower(d.MAC)] = d
	}
	return out
}

func TestThisPhoneIsMarkedSelfApprovedNeverAnArrival(t *testing.T) {
	c, _, box := gatedCore(t)
	const phone, stranger = "fa:00:00:00:00:86", "02:00:00:00:00:99"
	selfIP := "192.168.1.86"
	c.self.set(func(string) domain.IP { return domain.MustIP(selfIP) })

	markedList(t, c) // the first listing: the baseline
	box.Join(mockrouter.Station{MAC: phone, Hostname: "Mon-Telephone", IP: selfIP})
	box.Join(mockrouter.Station{MAC: stranger, Hostname: "Inconnu", IP: "192.168.1.99"})

	list := markedList(t, c)
	if d := list[phone]; d.Mark != "Self" || !d.Approved || d.NewlySeen {
		t.Fatalf("this phone = %+v, want marked Self, approved, not new", d)
	}
	if d := list[stranger]; d.Mark != "" || d.Approved || !d.NewlySeen {
		t.Fatalf("a device that joined = %+v, want unmarked, not approved, new", d)
	}
	for mac, d := range list {
		if mac != phone && d.Mark != "" {
			t.Fatalf("%s marked %q: only this phone is", mac, d.Mark)
		}
	}

	// A new address on the box: still this phone, by the MAC learnt from
	// the first match, as long as the phone is on that Wi-Fi.
	box.Join(mockrouter.Station{MAC: phone, Hostname: "Mon-Telephone", IP: "192.168.1.87"})
	if d := markedList(t, c)[phone]; d.Mark != "Self" || !d.Approved || d.NewlySeen {
		t.Fatalf("this phone after a new IP = %+v, want still Self", d)
	}

	// Off that Wi-Fi (no address on the box): nothing is this phone.
	c.self.set(func(string) domain.IP { return domain.IP{} })
	for mac, d := range markedList(t, c) {
		if d.Mark != "" {
			t.Fatalf("%s marked %q while the phone is off the box's Wi-Fi", mac, d.Mark)
		}
	}
}

// The demo simulates « ce téléphone »: a device of the demo box is this
// phone, so the app shows its hidden line there too.
func TestDemoHasThisPhone(t *testing.T) {
	c := approvalDemo(t)
	d, ok := markedList(t, c)[DemoPhoneMAC]
	if !ok || d.Mark != "Self" || !d.Approved || d.NewlySeen {
		t.Fatalf("the demo's phone = %+v (listed %v), want marked Self", d, ok)
	}
	// The owner's other phone is there too, marked Owner (#87).
	if o := markedList(t, c)[DemoOwnerPhoneMAC]; o.Mark != MarkOwner || !o.Approved || o.NewlySeen {
		t.Fatalf("the demo owner's other phone = %+v, want marked Owner", o)
	}
	// Auto-reject spares it like the real phone: the same address.
	if ip := c.self.SelfIP(t.Context(), c.DemoURL()); ip.String() != d.IP {
		t.Fatalf("demo self IP = %s, want the demo phone's %s", ip, d.IP)
	}
}
