package mobilecore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

const remoteMAC = "AC-BB-CC-00-00-22"

type remoteRelaysAnswer struct {
	Owner  bool `json:"owner"`
	Relays []struct {
		AgentID string `json:"agentId"`
		Kind    string `json:"kind"`
	} `json:"relays"`
}

func remoteRelays(t *testing.T, c *Core, tenant, bundle string) remoteRelaysAnswer {
	t.Helper()
	raw, err := c.RemoteRelays(tenant, bundle)
	if err != nil {
		t.Fatalf("RemoteRelays: %v", err)
	}
	var a remoteRelaysAnswer
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	return a
}

func TestRemoteRelays_OnlyTheOwnersBundleTurnsRemoteModeOn(t *testing.T) {
	c := newCore(t, nil)
	relay := newRelay(t)

	if a := remoteRelays(t, c, credTenant, ""); a.Owner || len(a.Relays) != 0 {
		t.Fatalf("no bundle: %+v, want local mode", a)
	}
	alone := bundleWith(t, c, credTenant)
	if alone == "" {
		alone, _ = c.SignTrustBundle(credTenant, "", "")
	}
	if a := remoteRelays(t, c, credTenant, alone); !a.Owner || len(a.Relays) != 0 {
		t.Fatalf("the owner alone: %+v, want owner and no relay", a)
	}
	withRelay := bundleWith(t, c, credTenant, relay)
	a := remoteRelays(t, c, credTenant, withRelay)
	if !a.Owner || len(a.Relays) != 1 || a.Relays[0].AgentID != agentName(0) {
		t.Fatalf("a paired relay: %+v", a)
	}

	// Another phone (a member, or this one reinstalled) did not sign it.
	other := newCore(t, nil)
	if a := remoteRelays(t, other, credTenant, withRelay); a.Owner || len(a.Relays) != 0 {
		t.Fatalf("another phone's view: %+v, want not the owner", a)
	}
	// Another household's bundle.
	if a := remoteRelays(t, c, "99999999-2222-3333-4444-555555555555", withRelay); a.Owner {
		t.Fatalf("another household: %+v", a)
	}
}

// signedSnapshot is what GET router/devices carries in "snapshot".
func signedSnapshot(t *testing.T, relay *trust.Identity, tenant, kind, agentID string, body string, at time.Time) string {
	t.Helper()
	s, err := trust.NewSnapshot(relay, tenant, kind, agentID, 1, []byte(body), at)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	return string(raw)
}

func TestOpenDeviceSnapshot_VerifiesTheRelaySignature(t *testing.T) {
	c := newCore(t, nil)
	relay := newRelay(t)
	bundle := bundleWith(t, c, credTenant, relay)
	at := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	list := `[{"MAC":"ac:bb:cc:00:00:22","Hostname":"Laptop-Bureau","Blocked":false}]`

	raw, err := c.OpenDeviceSnapshot(credTenant, bundle, signedSnapshot(t, relay, credTenant, "devices", agentName(0), list, at))
	if err != nil {
		t.Fatalf("OpenDeviceSnapshot: %v", err)
	}
	var got struct {
		AgentID string            `json:"agentId"`
		AsOf    time.Time         `json:"asOf"`
		Devices []json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.AgentID != agentName(0) || !got.AsOf.Equal(at) || len(got.Devices) != 1 {
		t.Fatalf("snapshot = %s", raw)
	}

	rogue := newRelay(t)
	cases := map[string]string{
		// A key the owner never signed in: what a hub making up a list has.
		"unknown key":          signedSnapshot(t, rogue, credTenant, "devices", agentName(0), list, at),
		"other household":      signedSnapshot(t, relay, "99999999-2222-3333-4444-555555555555", "devices", agentName(0), list, at),
		"not a device list":    signedSnapshot(t, relay, credTenant, "ssids", agentName(0), list, at),
		"another relay's name": signedSnapshot(t, relay, credTenant, "devices", "agent-z", list, at),
	}
	for name, snap := range cases {
		if _, err := c.OpenDeviceSnapshot(credTenant, bundle, snap); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// The hub swapped the list under the relay's signature.
	var s map[string]any
	_ = json.Unmarshal([]byte(signedSnapshot(t, relay, credTenant, "devices", agentName(0), list, at)), &s)
	payload, _ := base64.StdEncoding.DecodeString(s["payload"].(string))
	s["payload"] = base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(payload), "Laptop-Bureau", "Intrus", 1)))
	tampered, _ := json.Marshal(s)
	if _, err := c.OpenDeviceSnapshot(credTenant, bundle, string(tampered)); !errors.Is(err, trust.ErrBadSignature) {
		t.Errorf("tampered list: err = %v, want a bad signature", err)
	}

	// A phone that is not the owner's cannot vouch for any list.
	if _, err := newCore(t, nil).OpenDeviceSnapshot(credTenant, bundle, signedSnapshot(t, relay, credTenant, "devices", agentName(0), list, at)); err == nil {
		t.Error("another phone opened the owner's snapshot")
	}
}

type signedCommandAnswer struct {
	Payload   []byte    `json:"payload"`
	Sig       []byte    `json:"sig"`
	CmdID     string    `json:"cmdId"`
	MAC       string    `json:"mac"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func signCommand(t *testing.T, c *Core, action, boxID string) signedCommandAnswer {
	t.Helper()
	raw, err := c.SignDeviceCommand(credTenant, remoteMAC, action, boxID)
	if err != nil {
		t.Fatalf("SignDeviceCommand(%s): %v", action, err)
	}
	var a signedCommandAnswer
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSignDeviceCommand_TheRelayAcceptsWhatTheOwnerSigned(t *testing.T) {
	c := newCore(t, nil)
	relay := newRelay(t)
	bundle := bundleWith(t, c, credTenant, relay)

	// Unblocking is free: no token needed.
	a := signCommand(t, c, "unblock", "")
	if a.MAC != "ac:bb:cc:00:00:22" || a.CmdID == "" {
		t.Fatalf("answer = %+v", a)
	}
	if ttl := time.Until(a.ExpiresAt); ttl <= 0 || ttl > trust.MaxCommandTTL {
		t.Fatalf("expires in %s", ttl)
	}

	// The relay's own verifier, fed the owner's bundle, accepts it.
	var cur signedJSON
	_ = json.Unmarshal([]byte(bundle), &cur)
	owner, _ := c.ownerIdentity()
	v := trust.NewVerifier(credTenant, owner.Public().SigningKey(), time.Now().Add(-time.Minute))
	if err := v.AcceptBundle(trust.Signed[trust.Bundle]{Payload: cur.Payload, Sig: cur.Sig}); err != nil {
		t.Fatal(err)
	}
	cmd, err := v.VerifyCommand(trust.SignedCommand{Payload: a.Payload, Sig: a.Sig}, nil, time.Now())
	if err != nil {
		t.Fatalf("the relay refuses the owner's command: %v", err)
	}
	if cmd.Method != http.MethodPost || cmd.Path != "/api/devices/ac:bb:cc:00:00:22/unblock" || cmd.CmdID != a.CmdID {
		t.Fatalf("signed %+v", cmd)
	}

	if _, err := c.SignDeviceCommand(credTenant, remoteMAC, "wifi-block", ""); err == nil {
		t.Error("a command other than block / unblock was signed")
	}
	if _, err := c.SignDeviceCommand(credTenant, "not-a-mac", "unblock", ""); err == nil {
		t.Error("a malformed MAC was signed")
	}
	if _, err := c.SignDeviceCommand("", remoteMAC, "unblock", ""); err == nil {
		t.Error("a command without a household was signed")
	}
}

func TestSignDeviceCommand_BlockNeedsTheEntitlement(t *testing.T) {
	c := newCore(t, nil)
	clk := &fakeClock{t: gateT0}
	c.gate.now = clk.now
	if err := c.SetPublicKeys(base64.StdEncoding.EncodeToString(testPub)); err != nil {
		t.Fatal(err)
	}

	// No token: refused, and the message says so for the app's lock.
	_, err := c.SignDeviceCommand(credTenant, remoteMAC, "block", mockSerial)
	if !errors.Is(err, entitlement.ErrNotEntitled) || !strings.HasPrefix(err.Error(), "not_entitled") {
		t.Fatalf("no token: err = %v", err)
	}

	// A Basic token for this box: signed, without reaching any box (the core
	// is not even pointed at one).
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	if a := signCommand(t, c, "block", strings.ToLower(mockSerial)); a.CmdID == "" {
		t.Fatal("no command id")
	}
	// For another box: refused.
	if _, err := c.SignDeviceCommand(credTenant, remoteMAC, "block", "ZTEGOTHERBOX"); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatalf("other box: err = %v", err)
	}
	// Past expiry and grace: refused; unblocking still is not.
	clk.set(gateT0.Add(30 * gateDay))
	if _, err := c.SignDeviceCommand(credTenant, remoteMAC, "block", mockSerial); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatalf("expired: err = %v", err)
	}
	signCommand(t, c, "unblock", mockSerial)
}

func signedResult(t *testing.T, relay *trust.Identity, tenant, cmdID string, status int) string {
	t.Helper()
	r, err := trust.NewResult(relay, tenant, cmdID, 1, status, []byte(`{"status":"blocked"}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	return string(raw)
}

func TestVerifyCommandResult_OnlyTheRelaysSignatureCounts(t *testing.T) {
	c := newCore(t, nil)
	relay := newRelay(t)
	bundle := bundleWith(t, c, credTenant, relay)
	cmd := signCommand(t, c, "unblock", "")

	raw, err := c.VerifyCommandResult(credTenant, cmd.CmdID, bundle, signedResult(t, relay, credTenant, cmd.CmdID, http.StatusOK))
	if err != nil {
		t.Fatalf("VerifyCommandResult: %v", err)
	}
	var got struct {
		AgentID string `json:"agentId"`
		Status  int    `json:"status"`
	}
	_ = json.Unmarshal([]byte(raw), &got)
	if got.Status != http.StatusOK || got.AgentID != agentName(0) {
		t.Fatalf("result = %s", raw)
	}

	// The hub cannot report "blocked ✅" on its own: its key is no relay's.
	hubKey := newRelay(t)
	if _, err := c.VerifyCommandResult(credTenant, cmd.CmdID, bundle, signedResult(t, hubKey, credTenant, cmd.CmdID, http.StatusOK)); !errors.Is(err, trust.ErrUnknownKey) {
		t.Fatalf("forged result: err = %v", err)
	}
	// Nor reuse the answer to another command.
	if _, err := c.VerifyCommandResult(credTenant, cmd.CmdID, bundle, signedResult(t, relay, credTenant, "another-command", http.StatusOK)); err == nil {
		t.Fatal("the answer to another command was accepted")
	}
	if _, err := c.VerifyCommandResult(credTenant, cmd.CmdID, bundle, signedResult(t, relay, "99999999-2222-3333-4444-555555555555", cmd.CmdID, http.StatusOK)); err == nil {
		t.Fatal("another household's answer was accepted")
	}
	// The relay's refusal verifies too: it is shown as a refusal, not hidden.
	raw, err = c.VerifyCommandResult(credTenant, cmd.CmdID, bundle, signedResult(t, relay, credTenant, cmd.CmdID, http.StatusForbidden))
	if err != nil || !strings.Contains(raw, `"status":403`) {
		t.Fatalf("refusal: %s %v", raw, err)
	}
}
