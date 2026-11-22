package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// This is the acceptance test of household members (issue #42). The owner,
// signed in with their number, invites another number; the invite goes out
// on WhatsApp (the console provider here); that number signs in with its
// own WhatsApp code and is a member of the household, in the role it was
// invited with. A viewer reads the device list the relay pushed but cannot
// send the box a command. The owner changes the role, then removes the
// member, which ends their sessions on the household.

// membersPath is the household's members route.
func membersPath(tenantID, suffix string) string {
	p := "/api/v1/tenants/" + tenantID + "/members"
	if suffix != "" {
		p += "/" + suffix
	}
	return p
}

// snapshotBody opens the device snapshot of a GET router/devices answer.
func snapshotBody(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(body["snapshot"])
	var signed trust.SignedSnapshot
	if err := json.Unmarshal(raw, &signed); err != nil {
		return ""
	}
	snap, err := signed.Unverified()
	if err != nil {
		return ""
	}
	return string(snap.Body)
}

// member finds a userId in a members listing.
func member(t *testing.T, body map[string]any, userID string) map[string]any {
	t.Helper()
	list, _ := body["members"].([]any)
	for _, m := range list {
		if mm, _ := m.(map[string]any); str(mm, "userId") == userID {
			return mm
		}
	}
	return nil
}

func TestE2E_InviteAcceptMemberRolesAndRemoval(t *testing.T) {
	// Fresh numbers on every run: the database is shared.
	ownerNumber := "+22176" + randomDigits(t, 7)
	memberNumber := "+22175" + randomDigits(t, 7)
	strangerNumber := "+22170" + randomDigits(t, 7)
	const memberIP, strangerIP = "198.51.100.11", "198.51.100.12"

	w := newWorldWith(t, fastTimings, withPhoneOwner(ownerNumber))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := w.owner
	hubURL := w.hub.URL

	// The household has a box behind a relay, as in TestE2E_BlockThroughTheHub.
	owner.registerNetwork(w.boxPin)
	owner.publishBundle(w.boxPin)
	_, state, _ := w.startRelay(ctx, owner.invite("pc"), "pc", "192.168.1.50")
	owner.publishBundle(w.boxPin, trustRelay(state, "pc", t))
	owner.sealCredentials(state.AgentID, relayPublic(t, state).X25519)
	waitForDevices(t, w)

	// 1. The owner invites a number as a viewer. The message goes out on
	// WhatsApp, and the invite is listed as pending.
	st, body := owner.call("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": memberNumber, "role": "viewer"})
	if st != http.StatusCreated || str(body, "role") != "viewer" || str(body, "phone") != memberNumber {
		t.Fatalf("invite = %d %v", st, body)
	}
	var invited string
	for _, s := range w.notifier.Sent() {
		if s.To == alertsdomain.Phone(memberNumber) {
			invited = s.Text
		}
	}
	if !strings.Contains(invited, "Kergui") || !strings.Contains(invited, ownerNumber) || !strings.Contains(invited, "lecteur") {
		t.Fatalf("WhatsApp invite = %q", invited)
	}
	st, body = owner.call("GET", membersPath(owner.tenantID, ""), nil)
	if st != http.StatusOK {
		t.Fatalf("members = %d %v", st, body)
	}
	if inv, _ := body["invites"].([]any); len(inv) != 1 {
		t.Fatalf("pending invites = %v", body["invites"])
	}
	if you, _ := body["you"].(map[string]any); str(you, "role") != "owner" {
		t.Fatalf("the owner's own role = %v", body["you"])
	}
	// Inviting the household's own number, or a number twice as a
	// member, is refused; a bad number too.
	if st, body := owner.call("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": ownerNumber, "role": "admin"}); st != http.StatusConflict {
		t.Fatalf("inviting the owner = %d %v", st, body)
	}
	if st, _ := owner.call("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": "12", "role": "viewer"}); st != http.StatusBadRequest {
		t.Fatalf("inviting a bad number = %d", st)
	}
	if st, _ := owner.call("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": strangerNumber, "role": "owner"}); st != http.StatusBadRequest {
		t.Fatalf("inviting an owner = %d", st)
	}

	// 2. The number signs in with its own code: it lands in the household,
	// as a viewer, and lists the devices the relay pushed.
	m := otpSignIn(t, hubURL, w.notifier, memberIP, memberNumber, "Tecno Spark")
	if m.tenantID != owner.tenantID {
		t.Fatalf("the invited number signed in to %s, want the household %s", m.tenantID, owner.tenantID)
	}
	mcall := func(method, path string, in any) (int, map[string]any) {
		return callFrom(t, hubURL, memberIP, method, path, m.access, in)
	}
	st, body = mcall("GET", owner.tenantPath("router/devices"), nil)
	if st != http.StatusOK || !strings.Contains(snapshotBody(t, body), targetMAC) {
		t.Fatalf("the member's device list = %d %v", st, body)
	}
	st, body = owner.call("GET", membersPath(owner.tenantID, ""), nil)
	mem := member(t, body, m.userID)
	if st != http.StatusOK || str(mem, "role") != "viewer" || str(mem, "phone") != memberNumber {
		t.Fatalf("the member as the owner lists it = %v", body)
	}
	if inv, _ := body["invites"].([]any); len(inv) != 0 {
		t.Fatalf("the accepted invite is still pending: %v", inv)
	}
	membershipID := str(mem, "membershipId")

	// 3. A viewer cannot act on the box, nor on the household.
	signed := owner.signedCommand(targetMAC, "block") // a valid signature: only the role refuses it
	if st, body := mcall("POST", owner.tenantPath("router/devices/"+targetMAC+"/block"), signed); st != http.StatusForbidden {
		t.Fatalf("a viewer's block = %d %v", st, body)
	}
	if w.box.Blocked(targetMAC) {
		t.Fatal("a viewer's block reached the box")
	}
	if st, _ := mcall("PUT", owner.tenantPath("network"), map[string]any{"ssids": []string{"x"}, "bssids": []string{}, "routerPin": "x"}); st != http.StatusForbidden {
		t.Fatalf("a viewer's PUT network = %d", st)
	}
	if st, _ := mcall("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": strangerNumber, "role": "viewer"}); st != http.StatusForbidden {
		t.Fatalf("a viewer's invite = %d", st)
	}
	st, body = mcall("GET", membersPath(owner.tenantID, ""), nil)
	if you, _ := body["you"].(map[string]any); st != http.StatusOK || str(you, "role") != "viewer" {
		t.Fatalf("the viewer's own listing = %d %v", st, body)
	}
	if _, ok := body["invites"]; ok {
		t.Fatalf("a viewer sees the pending invites: %v", body["invites"])
	}

	// 4. The owner makes them an admin; an admin still may not manage the
	// members, and the owner's role cannot be changed.
	if st, body := owner.call("PATCH", membersPath(owner.tenantID, membershipID), map[string]any{"role": "admin"}); st != http.StatusOK || str(body, "role") != "admin" {
		t.Fatalf("change role = %d %v", st, body)
	}
	st, body = mcall("GET", membersPath(owner.tenantID, ""), nil)
	if you, _ := body["you"].(map[string]any); st != http.StatusOK || str(you, "role") != "admin" {
		t.Fatalf("after the change, the member's role = %d %v", st, body)
	}
	ownerMembership := str(member(t, body, owner.userID), "membershipId")
	if st, _ := mcall("DELETE", membersPath(owner.tenantID, ownerMembership), nil); st != http.StatusForbidden {
		t.Fatalf("an admin removing the owner = %d", st)
	}
	if st, _ := owner.call("PATCH", membersPath(owner.tenantID, ownerMembership), map[string]any{"role": "viewer"}); st != http.StatusBadRequest {
		t.Fatalf("changing the owner's role = %d", st)
	}

	// 5. Removal: the member's household routes answer 403 at once, their
	// refresh token for the household is dead, and their next sign-in
	// lands in a household of their own.
	if st, body := owner.call("DELETE", membersPath(owner.tenantID, membershipID), nil); st != http.StatusNoContent {
		t.Fatalf("remove = %d %v", st, body)
	}
	if st, _ := mcall("GET", owner.tenantPath("router/devices"), nil); st != http.StatusForbidden {
		t.Fatalf("a removed member's device list = %d, want 403", st)
	}
	if n := countRows(t, `SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL`,
		m.userID, owner.tenantID); n != 0 {
		t.Fatalf("%d live refresh tokens on the household after removal", n)
	}
	if st, _ := callFrom(t, hubURL, memberIP, "POST", "/api/v1/auth/refresh", "", map[string]any{"refreshToken": m.refreshToken}); st != http.StatusUnauthorized {
		t.Fatalf("a removed member's refresh = %d, want 401", st)
	}
	st, body = callFrom(t, hubURL, memberIP, "POST", "/licensing/v1/session/hub", m.session, nil)
	if st != http.StatusOK || str(body, "tenantId") == "" || str(body, "tenantId") == owner.tenantID {
		t.Fatalf("a removed member's new session = %d %v", st, body)
	}
	if st, body = owner.call("GET", membersPath(owner.tenantID, ""), nil); member(t, body, m.userID) != nil {
		t.Fatalf("the removed member is still listed: %v", body)
	}

	// 6. Invited again, the number rejoins on its next sign-in (here the
	// app's way back in, with its licensing session).
	if st, body := owner.call("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": memberNumber, "role": "admin"}); st != http.StatusCreated {
		t.Fatalf("re-invite = %d %v", st, body)
	}
	st, body = callFrom(t, hubURL, memberIP, "POST", "/licensing/v1/session/hub", m.session, nil)
	if st != http.StatusOK || str(body, "tenantId") != owner.tenantID {
		t.Fatalf("rejoining = %d %v", st, body)
	}
	if st, _ := callFrom(t, hubURL, memberIP, "GET", owner.tenantPath("router/devices"), str(body, "accessToken"), nil); st != http.StatusOK {
		t.Fatalf("the rejoined member's device list = %d", st)
	}
	_, body = owner.call("GET", membersPath(owner.tenantID, ""), nil)
	if mem := member(t, body, m.userID); str(mem, "role") != "admin" {
		t.Fatalf("the rejoined member = %v", mem)
	}

	// Every step is in the household's audit log, the kernel's enum
	// included (the role change is the hub's own action).
	for _, action := range []string{"MEMBER_INVITED", "MEMBER_ACCEPTED", "MEMBER_ROLE_CHANGED", "MEMBER_REVOKED"} {
		waitForRow(t, 20*time.Second, `SELECT count(*) FROM audit_logs WHERE tenant_id = $1 AND action = $2`, owner.tenantID, action)
	}

	// 7. A cancelled invite is not accepted.
	st, body = owner.call("POST", membersPath(owner.tenantID, "invites"), map[string]any{"phone": strangerNumber, "role": "viewer"})
	if st != http.StatusCreated {
		t.Fatalf("invite = %d %v", st, body)
	}
	if st, _ := owner.call("DELETE", membersPath(owner.tenantID, "invites/"+str(body, "id")), nil); st != http.StatusNoContent {
		t.Fatalf("cancel invite = %d", st)
	}
	s := otpSignIn(t, hubURL, w.notifier, strangerIP, strangerNumber, "Itel")
	if s.tenantID == owner.tenantID {
		t.Fatal("a cancelled invite was accepted")
	}
	if st, _ := callFrom(t, hubURL, strangerIP, "GET", membersPath(owner.tenantID, ""), s.access, nil); st != http.StatusForbidden {
		t.Fatalf("a stranger listing the members = %d, want 403", st)
	}
}
