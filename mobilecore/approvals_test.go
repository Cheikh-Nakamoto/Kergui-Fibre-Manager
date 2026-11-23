package mobilecore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
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
		s.settle(box, nil, t0, false) // the first listing: nothing there
		return s
	}
	approved := func(s *approvalStore) bool { b, _ := s.get(box); return contains(b.MACs, m) }

	s := fresh()
	s.settle(box, on, t0, false)
	s.settle(box, []listedDevice{{MAC: m, Connected: true, Blocked: true}}, t0.Add(time.Minute), false)
	s.settle(box, on, late, false)
	if approved(s) {
		t.Error("blocked within the delay, then unblocked: approved without a new delay")
	}
	if s.settle(box, on, late.Add(AutoApproveDelay), false); !approved(s) {
		t.Error("unblocked and there for the delay again: not approved")
	}

	s = fresh()
	s.settle(box, on, t0, true)
	if s.settle(box, on, late, true); approved(s) {
		t.Error("approved by itself while auto-reject is on")
	}

	s = fresh()
	s.settle(box, on, t0, false)
	if s.settle(box, []listedDevice{{MAC: m}}, late, false); approved(s) {
		t.Error("approved while gone or cut off the Wi-Fi")
	}

	s = fresh()
	s.approve(box, m)
	s.withdraw(box, m)
	s.settle(box, on, t0, false)
	if s.settle(box, on, late, false); approved(s) {
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
