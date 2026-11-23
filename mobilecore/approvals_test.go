package mobilecore

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Approvals (#58): the devices the household approved never alert. The core
// keeps them per box, the device list says which are, a device named before
// the upgrade is approved, and the demo can make a visitor join and leave.

type approvedDevice struct {
	MAC        string
	CustomName string
	Connected  bool
	Approved   bool
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

func TestApproveAndWithdraw(t *testing.T) {
	c := approvalDemo(t)
	if approvedList(t, c)[laptop].Approved {
		t.Fatal("a device nobody approved or named is approved")
	}
	if status, body := call(t, c, "PUT", "/api/approvals/AC:BB:CC:00:00:22", ""); status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, body)
	}
	if !approvedList(t, c)[laptop].Approved {
		t.Fatal("not approved after « Approuver »")
	}
	a := approvals(t, c)
	if len(a.MACs) != 1 || a.MACs[0] != laptop || len(a.Unsynced) != 1 {
		t.Fatalf("approvals = %+v", a)
	}
	if status, body := call(t, c, "DELETE", "/api/approvals/"+laptop, ""); status != http.StatusOK {
		t.Fatalf("withdraw = %d %s", status, body)
	}
	if approvedList(t, c)[laptop].Approved {
		t.Fatal("still approved after withdrawing")
	}
	if status, _ := call(t, c, "PUT", "/api/approvals/not-a-mac", ""); status != http.StatusBadRequest {
		t.Fatalf("approve a bad MAC = %d, want 400", status)
	}
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

func TestRenamedBeforeUpgradeIsApproved(t *testing.T) {
	c := approvalDemo(t)
	// A name given before approvals existed: the store has no record of this
	// box yet, so the next listing approves every named device once.
	approvedList(t, c)
	if status, body := call(t, c, "PATCH", "/api/devices/"+laptop, `{"custom_name":"Bureau"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}
	c.approvals.forget(c.boxID())
	if !approvedList(t, c)[laptop].Approved {
		t.Fatal("a device named before the upgrade is not approved")
	}
	if a := approvals(t, c); len(a.Unsynced) != 1 {
		t.Fatalf("the upgrade's approvals are not left for the hub: %+v", a)
	}
	// Once done, a withdrawn named device stays withdrawn.
	call(t, c, "DELETE", "/api/approvals/"+laptop, "")
	if approvedList(t, c)[laptop].Approved {
		t.Fatal("the upgrade ran again")
	}
}

func TestRenameApproves(t *testing.T) {
	c := approvalDemo(t)
	approvedList(t, c) // the upgrade is done: nothing named yet
	if status, body := call(t, c, "PATCH", "/api/devices/"+laptop, `{"custom_name":"Bureau"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}
	if !approvedList(t, c)[laptop].Approved {
		t.Fatal("a renamed device is not approved")
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
