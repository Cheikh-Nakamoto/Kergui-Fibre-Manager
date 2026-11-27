package mobilecore

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// Approvals (#58): the devices the household approved never alert. The core
// keeps them per box, the device list says which are, the devices already
// there and those left alone for 15 minutes are approved by themselves
// (owner rule, 2026-11-23), and the demo can make a visitor join and leave.

type approvedDevice struct {
	MAC        string
	CustomName string
	Connected  bool
	Approved   bool
	NewlySeen  bool
}

type approvalsAnswer struct {
	MACs     []string `json:"macs"`
	Unsynced []string `json:"unsynced"`
}

func approvalDemo(t *testing.T) *Core {
	t.Helper()
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"`+DemoUsername+`","password":"`+DemoPassword+`"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	return c
}

func approvedList(t *testing.T, c *Core) map[string]approvedDevice {
	t.Helper()
	status, body := call(t, c, "GET", "/api/devices", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/devices = %d %s", status, body)
	}
	var list []approvedDevice
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	out := map[string]approvedDevice{}
	for _, d := range list {
		out[strings.ToLower(d.MAC)] = d
	}
	return out
}

func approvals(t *testing.T, c *Core) approvalsAnswer {
	t.Helper()
	status, body := call(t, c, "GET", "/api/approvals", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/approvals = %d %s", status, body)
	}
	var out approvalsAnswer
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return out
}

const laptop = "ac:bb:cc:00:00:22" // Laptop-Bureau on the demo F6600P

func TestPresentDevicesAreApprovedOnce(t *testing.T) {
	c := approvalDemo(t)
	d := approvedList(t, c)[laptop]
	if !d.Approved || d.NewlySeen {
		t.Fatalf("a device present at the first listing = %+v, want approved and not new", d)
	}
	if a := approvals(t, c); len(a.Unsynced) == 0 {
		t.Fatalf("the first listing's approvals are not left for the hub: %+v", a)
	}
	// Done once: a withdrawn device stays withdrawn.
	call(t, c, "DELETE", "/api/approvals/"+laptop, "")
	if approvedList(t, c)[laptop].Approved {
		t.Fatal("the first listing's approval ran again")
	}
}

func TestApproveAndWithdraw(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c)
	if status, body := call(t, c, "DELETE", "/api/approvals/"+laptop, ""); status != http.StatusOK {
		t.Fatalf("withdraw = %d %s", status, body)
	}
	if approvedList(t, c)[laptop].Approved {
		t.Fatal("still approved after withdrawing")
	}
	if status, body := call(t, c, "PUT", "/api/approvals/AC:BB:CC:00:00:22", ""); status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, body)
	}
	if !approvedList(t, c)[laptop].Approved {
		t.Fatal("not approved after « Approuver »")
	}
	a := approvals(t, c)
	if !contains(a.MACs, laptop) || !contains(a.Unsynced, laptop) {
		t.Fatalf("approvals = %+v", a)
	}
	if status, _ := call(t, c, "PUT", "/api/approvals/not-a-mac", ""); status != http.StatusBadRequest {
		t.Fatalf("approve a bad MAC = %d, want 400", status)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestSetApprovalsReplacesAndMarksSynced(t *testing.T) {
	c := approvalDemo(t)
	call(t, c, "PUT", "/api/approvals/"+laptop, "")
	if status, body := call(t, c, "PUT", "/api/approvals", `{"macs":["AC:BB:CC:00:00:33"]}`); status != http.StatusOK {
		t.Fatalf("set = %d %s", status, body)
	}
	a := approvals(t, c)
	if len(a.MACs) != 1 || a.MACs[0] != "ac:bb:cc:00:00:33" || len(a.Unsynced) != 0 {
		t.Fatalf("approvals after the hub's list = %+v", a)
	}
}

func TestRenameApproves(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c)
	call(t, c, "DELETE", "/api/approvals/"+laptop, "")
	if status, body := call(t, c, "PATCH", "/api/devices/"+laptop, `{"custom_name":"Bureau"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}
	if !approvedList(t, c)[laptop].Approved {
		t.Fatal("a renamed device is not approved")
	}
}

// A device that joins is signalled (not approved), then approved by itself
// once it has been there AutoApproveDelay.
func TestJoinedDeviceIsApprovedAfterTheDelay(t *testing.T) {
	c := approvalDemo(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	c.clock.set(func() time.Time { return now })
	approvedList(t, c)
	call(t, c, "POST", "/api/demo/visitor", `{"present":true}`)
	if d := approvedList(t, c)[DemoVisitorMAC]; d.Approved {
		t.Fatal("a device that just joined is approved")
	}
	now = now.Add(AutoApproveDelay - time.Minute)
	if approvedList(t, c)[DemoVisitorMAC].Approved {
		t.Fatal("approved before the delay")
	}
	now = now.Add(time.Minute)
	if !approvedList(t, c)[DemoVisitorMAC].Approved {
		t.Fatal("not approved after the delay")
	}
	if a := approvals(t, c); !contains(a.Unsynced, DemoVisitorMAC) {
		t.Fatalf("the approval is not left for the hub: %+v", a)
	}
}

func TestOwnerRuleSpares(t *testing.T) {
	const box, m = "box", "02:00:00:00:00:01"
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	late := t0.Add(AutoApproveDelay)
	on := []listedDevice{{MAC: m, Connected: true}}
	fresh := func() *approvalStore {
		s := openApprovals(t.TempDir())
		s.settle(box, nil, t0, ownerRule{}) // the first listing: nothing there
		return s
	}
	approved := func(s *approvalStore) bool { b, _ := s.get(box); return contains(b.MACs, m) }

	s := fresh()
	s.settle(box, on, t0, ownerRule{})
	s.settle(box, []listedDevice{{MAC: m, Connected: true, Blocked: true}}, t0.Add(time.Minute), ownerRule{})
	s.settle(box, on, late, ownerRule{})
	if approved(s) {
		t.Error("blocked within the delay, then unblocked: approved without a new delay")
	}
	if s.settle(box, on, late.Add(AutoApproveDelay), ownerRule{}); !approved(s) {
		t.Error("unblocked and there for the delay again: not approved")
	}

	s = fresh()
	s.settle(box, on, t0, ownerRule{autoReject: true})
	if s.settle(box, on, late, ownerRule{autoReject: true}); approved(s) {
		t.Error("approved by itself while auto-reject is on")
	}

	s = fresh()
	s.settle(box, on, t0, ownerRule{})
	if s.settle(box, []listedDevice{{MAC: m}}, late, ownerRule{}); approved(s) {
		t.Error("approved while gone or cut off the Wi-Fi")
	}

	s = fresh()
	s.approve(box, m)
	s.withdraw(box, m)
	s.settle(box, on, t0, ownerRule{})
	if s.settle(box, on, late, ownerRule{}); approved(s) {
		t.Error("a withdrawn approval came back by itself")
	}
	if s.approve(box, m); !approved(s) {
		t.Error("« Approuver » by hand no longer works")
	}
}

func TestApprovalsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(dir, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Configure("http://192.168.1.1", "", ""); err != nil {
		t.Fatal(err)
	}
	call(t, c, "PUT", "/api/approvals/"+laptop, "")
	_ = c.Close()
	c2, err := Open(dir, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Configure("http://192.168.1.1", "", ""); err != nil {
		t.Fatal(err)
	}
	if a := approvals(t, c2); len(a.MACs) != 1 {
		t.Fatalf("approvals after reopening = %+v", a)
	}
}

func TestDemoVisitorJoinsAndLeaves(t *testing.T) {
	c := approvalDemo(t)
	if _, ok := approvedList(t, c)[DemoVisitorMAC]; ok {
		t.Fatal("the visitor is there before joining")
	}
	if status, body := call(t, c, "POST", "/api/demo/visitor", `{"present":true}`); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}
	if d, ok := approvedList(t, c)[DemoVisitorMAC]; !ok || !d.Connected || d.Approved {
		t.Fatalf("visitor after joining = %+v (listed %v)", d, ok)
	}
	if status, body := call(t, c, "POST", "/api/demo/visitor", `{"present":false}`); status != http.StatusOK {
		t.Fatalf("leave = %d %s", status, body)
	}
	if _, ok := approvedList(t, c)[DemoVisitorMAC]; ok {
		t.Fatal("the visitor is still listed after leaving")
	}
}

func TestDemoVisitorOnlyInTheDemo(t *testing.T) {
	c := newCore(t, nil)
	if err := c.Configure("http://192.168.1.1", "", ""); err != nil {
		t.Fatal(err)
	}
	if status, _ := call(t, c, "POST", "/api/demo/visitor", `{"present":true}`); status != http.StatusNotFound {
		t.Fatalf("visitor on a real box = %d, want 404", status)
	}
}

// RFC-007 D1, G6: a device approves itself after the delay only while the
// Wi-Fi is open. Closed, or during an add window, only a hand « Autoriser »
// lets it in, and its delay starts again once the Wi-Fi is open.
func TestOwnerRule_NoSelfApprovalWhileTheWiFiIsClosed(t *testing.T) {
	const box, m = "box", "02:00:00:00:00:01"
	t0 := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	on := []listedDevice{{MAC: m, Connected: true}}
	approved := func(s *approvalStore) bool { b, _ := s.get(box); return contains(b.MACs, m) }
	closed := ownerRule{wifiClosed: true}

	s := openApprovals(t.TempDir())
	s.settle(box, nil, t0, ownerRule{}) // the first listing: nothing there
	s.settle(box, on, t0, ownerRule{})  // timed while open
	if s.settle(box, on, t0.Add(AutoApproveDelay), closed); approved(s) {
		t.Fatal("approved by itself while the Wi-Fi is closed")
	}
	if s.settle(box, on, t0.Add(2*AutoApproveDelay), ownerRule{}); approved(s) {
		t.Fatal("approved at once on opening: its delay ran while closed")
	}
	if s.settle(box, on, t0.Add(3*AutoApproveDelay), ownerRule{}); !approved(s) {
		t.Fatal("open again for the delay: not approved")
	}

	// The first listing of a box found closed approves nobody; it waits
	// for the Wi-Fi to be open.
	s = openApprovals(t.TempDir())
	if added := s.settle(box, on, t0, closed); len(added) != 0 || approved(s) {
		t.Fatalf("the first listing approved %v while closed", added)
	}
	if s.settle(box, on, t0.Add(time.Minute), ownerRule{}); !approved(s) {
		t.Fatal("the first listing once open did not approve the devices present")
	}

	// « Autoriser » by hand still works while closed.
	s = openApprovals(t.TempDir())
	s.settle(box, nil, t0, closed)
	if s.approve(box, m); !approved(s) {
		t.Fatal("« Autoriser » by hand refused while closed")
	}
}

// The same through the core: the stored intent says closed, or an add window
// runs, and the visitor stays « À vérifier » past the delay.
func TestJoinedDeviceIsNotApprovedWhileTheWiFiIsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent func(now time.Time) domain.WiFiLockIntent
	}{
		{"closed", func(now time.Time) domain.WiFiLockIntent { return domain.WiFiLockIntent{Closed: true, Since: now} }},
		{"add window", func(now time.Time) domain.WiFiLockIntent {
			return domain.WiFiLockIntent{Closed: true, Since: now, WindowUntil: now.Add(time.Hour)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := approvalDemo(t)
			now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
			c.clock.set(func() time.Time { return now })
			approvedList(t, c)
			if err := c.wifiLock.SaveWiFiLockIntent(context.Background(), c.boxID(), tc.intent(now)); err != nil {
				t.Fatal(err)
			}
			call(t, c, "POST", "/api/demo/visitor", `{"present":true}`)
			approvedList(t, c)
			now = now.Add(AutoApproveDelay + time.Minute)
			if approvedList(t, c)[DemoVisitorMAC].Approved {
				t.Fatal("a device approved itself while the Wi-Fi is closed")
			}
			if status, _ := call(t, c, "PUT", "/api/approvals/"+DemoVisitorMAC, ""); status != http.StatusOK || !approvedList(t, c)[DemoVisitorMAC].Approved {
				t.Fatal("« Autoriser » by hand does not work while closed")
			}
		})
	}
}

// Auto-reject's accepted set is fed from the approvals and the owner's
// phones (RFC-007 D1): one piece of data behind « Autorisé ».
func TestApprovalsFeedAutoReject(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c)
	call(t, c, "PUT", "/api/approvals/"+DemoVisitorMAC, "")
	c.owners.set(c.boxID(), []string{"02:4b:47:00:00:60"})
	macs, err := c.coreApprovals().ApprovedMACs(context.Background(), c.boxID())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range macs {
		got[m.String()] = true
	}
	if !got[DemoVisitorMAC] || !got[laptop] || !got["02:4b:47:00:00:60"] {
		t.Fatalf("approved for auto-reject = %v, want the visitor, the laptop and the owner's phone", macs)
	}
	if other, _ := c.coreApprovals().ApprovedMACs(context.Background(), "another-box"); len(other) != 0 {
		t.Fatalf("another box's approvals = %v", other)
	}
}
