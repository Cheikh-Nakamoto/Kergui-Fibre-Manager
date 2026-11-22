package e2e

// Member delegations (issue #47). The owner's phone and a member's phone are
// both real mobilecore.Cores. The member's core creates its key and the app
// publishes it on PUT members/me/key; the owner's screen and the member's
// show the same six digits; the owner's core signs a control delegation
// into the bundle; the member's core then reads the relay's signed list and
// signs a block with its own key, which the PC relay runs on the mock
// F6600P. Owner-only and revocation stop it, at the member's core and at the
// hub; the relay's own refusals (expired, revoked, owner-only, view writing)
// are in internal/agent/delegation_test.go, where the clock is the test's.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/mobilecore"
)

// memberPhone is a member's app: its household session and its own core.
type memberPhone struct {
	t        *testing.T
	core     *mobilecore.Core
	call     func(method, path string, in any) (int, map[string]any)
	tenantID string
}

func (m *memberPhone) bundle() string {
	m.t.Helper()
	code, body := m.call("GET", "/api/v1/tenants/"+m.tenantID+"/trust-bundle", nil)
	if code != http.StatusOK {
		m.t.Fatalf("member GET trust-bundle = %d %v", code, body)
	}
	b, _ := json.Marshal(body)
	return string(b)
}

type memberRemote struct {
	Relays []struct {
		AgentID string `json:"agentId"`
	} `json:"relays"`
	OwnerOnly bool `json:"ownerOnly"`
	Member    struct {
		State   string `json:"state"`
		Scope   string `json:"scope"`
		Control bool   `json:"control"`
		Code    string `json:"code"`
	} `json:"member"`
}

func (m *memberPhone) remote() memberRemote {
	m.t.Helper()
	raw, err := m.core.RemoteRelays(m.tenantID, m.bundle())
	if err != nil {
		m.t.Fatal(err)
	}
	var r memberRemote
	_ = json.Unmarshal([]byte(raw), &r)
	return r
}

// command signs a block / unblock in the member's core against the bundle
// given and POSTs it; err is the core's refusal.
func (m *memberPhone) command(bundle, action string) (int, map[string]any, string, error) {
	m.t.Helper()
	raw, err := m.core.SignMemberCommand(m.tenantID, bundle, targetMAC, action, mockBoxSerial)
	if err != nil {
		return 0, nil, "", err
	}
	var signed struct {
		Payload []byte `json:"payload"`
		Sig     []byte `json:"sig"`
		CmdID   string `json:"cmdId"`
		MAC     string `json:"mac"`
	}
	_ = json.Unmarshal([]byte(raw), &signed)
	code, body := m.call("POST", "/api/v1/tenants/"+m.tenantID+"/router/devices/"+signed.MAC+"/"+action,
		map[string]any{"payload": signed.Payload, "sig": signed.Sig})
	return code, body, signed.CmdID, nil
}

func TestE2E_MemberDelegationFromKeyToBlock(t *testing.T) {
	ownerNumber := "+22176" + randomDigits(t, 7)
	memberNumber := "+22175" + randomDigits(t, 7)
	const memberIP = "198.51.100.21"

	w := newWorldWith(t, fastTimings, withPhoneOwner(ownerNumber))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The owner's phone pairs the PC relay and seals the box password, as
	// in TestE2E_RemoteModeBlockFromTheOwnersCore.
	owner := newPhoneOwnerApp(t, w.owner)
	if err := owner.core.Configure(w.boxURL, "zte_f6600p", w.boxPin); err != nil {
		t.Fatal(err)
	}
	owner.loginToBox(boxPass)
	w.owner.registerNetwork(w.boxPin)
	owner.signAndUpload(map[string]any{"routerPin": w.boxPin})
	inv, err := trust.ParseInvite(owner.showQR("pc"))
	if err != nil {
		t.Fatal(err)
	}
	state, res, err := agent.Enroll(ctx, w.hub.URL, inv, "PC du salon", "pc", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	listed := owner.agent(res.AgentID)
	if owner.ownerCode(listed) != res.Code {
		t.Fatal("the two pairing codes differ")
	}
	owner.confirm(listed)
	owner.uploadSealed()
	handler, vlt := newRouterAPI(t, w.boxURL, w.boxPin)
	ag := w.newAgent(state, "192.168.1.50", handler, vlt)
	go func() { _ = ag.Run(ctx) }()
	waitForDevices(t, w)
	waitForRelayStatus(t, w, "active")

	// The owner invites a number as an admin; it signs in on its phone.
	if st, body := w.owner.call("POST", membersPath(w.owner.tenantID, "invites"), map[string]any{"phone": memberNumber, "role": "admin"}); st != http.StatusCreated {
		t.Fatalf("invite = %d %v", st, body)
	}
	signIn := otpSignIn(t, w.hub.URL, w.notifier, memberIP, memberNumber, "Tecno Spark")
	if signIn.tenantID != w.owner.tenantID {
		t.Fatalf("the member signed in to %s", signIn.tenantID)
	}
	mcore, err := mobilecore.Open(t.TempDir(), "member-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mcore.Close() })
	m := &memberPhone{t: t, core: mcore, tenantID: signIn.tenantID, call: func(method, path string, in any) (int, map[string]any) {
		return callFrom(t, w.hub.URL, memberIP, method, path, signIn.access, in)
	}}

	// 1. The member's key is created in its core; the app publishes it.
	raw, err := mcore.MemberKeys()
	if err != nil {
		t.Fatal(err)
	}
	var keys struct {
		KeyID   string `json:"keyId"`
		Ed25519 []byte `json:"ed25519"`
		X25519  []byte `json:"x25519"`
	}
	_ = json.Unmarshal([]byte(raw), &keys)
	if st, body := m.call("PUT", membersPath(m.tenantID, "me/key"), map[string]any{"ed25519": keys.Ed25519, "x25519": keys.X25519}); st != http.StatusNoContent {
		t.Fatalf("PUT members/me/key = %d %v", st, body)
	}
	if st, _ := m.call("PUT", membersPath(m.tenantID, "me/key"), map[string]any{"ed25519": []byte{1}, "x25519": keys.X25519}); st != http.StatusBadRequest {
		t.Fatalf("a malformed key = %d, want 400", st)
	}

	// Before any delegation the member stays local, and cannot act.
	if r := m.remote(); len(r.Relays) != 0 || r.Member.State != "none" {
		t.Fatalf("before the delegation: %+v", r)
	}
	if _, _, _, err := m.command(m.bundle(), "unblock"); err == nil || !strings.HasPrefix(err.Error(), "not_delegated") {
		t.Fatalf("a member with no delegation signed: %v", err)
	}

	// 2. The owner lists the member with its key; both screens show the
	// same six digits.
	st, body := w.owner.call("GET", membersPath(w.owner.tenantID, ""), nil)
	listedMember := member(t, body, signIn.userID)
	listedKeys, _ := listedMember["keys"].(map[string]any)
	if st != http.StatusOK || str(listedKeys, "keyId") != keys.KeyID {
		t.Fatalf("the owner's members list = %d %v", st, body)
	}
	ownerSide, err := owner.core.PairingCode(str(listedKeys, "ed25519"), str(listedKeys, "x25519"))
	if err != nil {
		t.Fatal(err)
	}
	if code := m.remote().Member.Code; code == "" || code != ownerSide {
		t.Fatalf("member code %q, owner code %q", code, ownerSide)
	}

	// 3. The owner's core delegates control to that key.
	ed, _ := base64.StdEncoding.DecodeString(str(listedKeys, "ed25519"))
	x, _ := base64.StdEncoding.DecodeString(str(listedKeys, "x25519"))
	owner.signAndUpload(map[string]any{"delegate": map[string]any{"ed25519": ed, "x25519": x, "scope": "control", "label": memberNumber}})

	r := m.remote()
	if len(r.Relays) != 1 || r.Relays[0].AgentID != res.AgentID || r.Member.State != "active" || !r.Member.Control {
		t.Fatalf("the member after the delegation: %+v", r)
	}
	// The member reads the relay's signed list in its own core.
	_, devBody := m.call("GET", "/api/v1/tenants/"+m.tenantID+"/router/devices", nil)
	snap, _ := json.Marshal(devBody["snapshot"])
	if _, err := mcore.OpenDeviceSnapshot(m.tenantID, m.bundle(), string(snap)); err != nil {
		t.Fatalf("the member opening the relay's list: %v", err)
	}

	// 4. The member blocks, signed with its own key; the relay runs it and
	// the member's core believes the relay's result. Unblocking is free, so
	// no pass is needed on the member's phone for it; block first needs one.
	if _, _, _, err := m.command(m.bundle(), "block"); err == nil || !strings.HasPrefix(err.Error(), "not_entitled") {
		t.Fatalf("a member's block with no pass = %v", err)
	}
	givePass(t, mcore)
	// The relay picks the new bundle up at its next heartbeat; until then
	// it refuses the member's key (a signed 403), so the app would retry.
	deadline := time.Now().Add(15 * time.Second)
	for {
		code, cbody, cmdID, err := m.command(m.bundle(), "block")
		if err != nil {
			t.Fatal(err)
		}
		if code == http.StatusAccepted {
			waitForCommandDone(t, w, cmdID)
			_, cbody = m.call("GET", "/api/v1/tenants/"+m.tenantID+"/router/commands/"+cmdID, nil)
		} else if code != http.StatusOK {
			t.Fatalf("the member's block = %d %v", code, cbody)
		}
		resRaw, _ := json.Marshal(cbody["result"])
		out, err := mcore.VerifyCommandResult(m.tenantID, cmdID, m.bundle(), string(resRaw))
		if err != nil {
			t.Fatalf("the member verifying the result: %v", err)
		}
		if strings.Contains(out, `"status":200`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the relay never ran the member's block: %s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("the member's block did not reach the box")
	}

	// 5. Owner-only: the member's core signs nothing, and a command it
	// signed before is refused by the hub too.
	before := m.bundle()
	owner.signAndUpload(map[string]any{"ownerOnly": true})
	if r := m.remote(); !r.OwnerOnly || r.Member.Control {
		t.Fatalf("owner-only, the member: %+v", r)
	}
	if _, _, _, err := m.command(m.bundle(), "unblock"); err == nil || !strings.HasPrefix(err.Error(), "owner_only") {
		t.Fatalf("a member's unblock in an owner-only household = %v", err)
	}
	if code, body, _, err := m.command(before, "unblock"); err != nil || code != http.StatusForbidden {
		t.Fatalf("a command signed on the old bundle = %d %v %v, want 403 from the hub", code, body, err)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("an owner-only household's device was unblocked by a member")
	}
	owner.signAndUpload(map[string]any{"ownerOnly": false})

	// 6. The owner revokes the delegation: the member goes back to local,
	// and what it signed before is refused.
	before = m.bundle()
	owner.signAndUpload(map[string]any{"revoke": keys.KeyID})
	if r := m.remote(); r.Member.State != "revoked" || len(r.Relays) != 0 {
		t.Fatalf("revoked, the member: %+v", r)
	}
	if code, body, _, err := m.command(before, "unblock"); err != nil || code != http.StatusForbidden {
		t.Fatalf("a revoked member's command = %d %v %v, want 403", code, body, err)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("a revoked member unblocked a device")
	}
}

// givePass gives a core a Basic pass for the mock box, signed by a key it
// is told to trust.
func givePass(t *testing.T, c *mobilecore.Core) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetPublicKeys(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatal(err)
	}
	tok, err := entitlement.Sign(priv, entitlement.Claims{
		Account: "acc-member", BoxID: mockBoxSerial, Tier: entitlement.TierBasic,
		IssuedAt: time.Now().Add(-time.Hour), ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	c.SetEntitlement(tok)
}
