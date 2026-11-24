package e2e

import (
	"crypto/rand"
	"net/http"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// ownerDevices reads the owner's phones from the household's inventory, as
// every phone of the household fetches them (issue #87).
func ownerDevices(t *testing.T, call func(method, path string, in any) (int, map[string]any), path string) []string {
	t.Helper()
	status, body := call(http.MethodGet, path, nil)
	if status != http.StatusOK {
		t.Fatalf("inventory = %d %v", status, body)
	}
	list, ok := body["owner_devices"].([]any)
	if !ok {
		t.Fatalf("the inventory has no owner_devices list: %v", body)
	}
	var macs []string
	for _, o := range list {
		macs = append(macs, str(o.(map[string]any), "mac"))
	}
	return macs
}

// TestE2E_OwnerDevicesDeclaredReadAndDroppedOnRecovery is issue #87 end to
// end: the owner's phone declares the MAC it has on the box, a member's
// household fetch returns it, a member cannot declare one, and once the
// household is handed to a new owner key (recovery) the old MAC is gone.
func TestE2E_OwnerDevicesDeclaredReadAndDroppedOnRecovery(t *testing.T) {
	ownerNumber := "+22176" + randomDigits(t, 7)
	memberNumber := "+22175" + randomDigits(t, 7)
	const memberIP = "198.51.100.21"

	w := newWorldWith(t, fastTimings, withPhoneOwner(ownerNumber))
	owner := w.owner
	owner.registerNetwork(w.boxPin)
	owner.publishBundle(w.boxPin)

	const oldPhone, newPhone = "fa:4b:47:00:00:01", "fa:4b:47:00:00:02"
	declare := func(mac, key string) (int, map[string]any) {
		return owner.call(http.MethodPut, owner.tenantPath("router/owner-devices"), map[string]any{"mac": mac, "ownerKey": key})
	}

	// Before anything is declared, the list is there and empty.
	if got := ownerDevices(t, owner.call, owner.tenantPath("router/inventory")); len(got) != 0 {
		t.Fatalf("owner devices before any declaration = %v", got)
	}

	// 1. The owner's phone saw itself on the box: it declares its MAC,
	// under the owner key that signed the bundle in force.
	if st, body := declare("FA-4B-47-00-00-01", owner.keys.KeyID()); st != http.StatusOK || str(body, "mac") != oldPhone {
		t.Fatalf("declare = %d %v", st, body)
	}
	// A key that did not sign the bundle (an admin's own phone, an old
	// owner) declares nothing.
	other, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if st, body := declare("aa:bb:cc:dd:ee:ff", other.KeyID()); st != http.StatusConflict {
		t.Fatalf("declare under another key = %d %v, want 409", st, body)
	}
	if st, _ := declare("not-a-mac", owner.keys.KeyID()); st != http.StatusBadRequest {
		t.Fatalf("declare a bad MAC = %d, want 400", st)
	}

	// 2. A member of the household reads it with the household data.
	if st, body := owner.call(http.MethodPost, membersPath(owner.tenantID, "invites"), map[string]any{"phone": memberNumber, "role": "viewer"}); st != http.StatusCreated {
		t.Fatalf("invite = %d %v", st, body)
	}
	m := otpSignIn(t, w.hub.URL, w.notifier, memberIP, memberNumber, "Tecno Spark")
	if m.tenantID != owner.tenantID {
		t.Fatalf("the member signed in to %s, want %s", m.tenantID, owner.tenantID)
	}
	mcall := func(method, path string, in any) (int, map[string]any) {
		return callFrom(t, w.hub.URL, memberIP, method, path, m.access, in)
	}
	if got := ownerDevices(t, mcall, owner.tenantPath("router/inventory")); len(got) != 1 || got[0] != oldPhone {
		t.Fatalf("the member reads owner devices %v, want [%s]", got, oldPhone)
	}

	// 3. A member cannot declare one, even with the owner's key id.
	if st, body := mcall(http.MethodPut, owner.tenantPath("router/owner-devices"), map[string]any{"mac": "aa:bb:cc:dd:ee:01", "ownerKey": owner.keys.KeyID()}); st != http.StatusForbidden {
		t.Fatalf("a member's declaration = %d %v, want 403", st, body)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_inventory_owner_devices WHERE tenant_id = $1`, owner.tenantID); n != 1 {
		t.Fatalf("%d owner devices stored, want the owner's one", n)
	}

	// 4. Recovery: the household is handed to a new owner key (the new
	// phone's), which signs the next bundle. The old phone's MAC is no
	// longer the owner's, before the new phone has declared anything.
	oldKey := owner.keys.KeyID()
	if owner.keys, err = trust.Generate(rand.Reader); err != nil {
		t.Fatal(err)
	}
	owner.publishBundle(w.boxPin)
	if got := ownerDevices(t, mcall, owner.tenantPath("router/inventory")); len(got) != 0 {
		t.Fatalf("after recovery the member reads owner devices %v, want none", got)
	}
	// The old key cannot put its phone back.
	if st, _ := declare(oldPhone, oldKey); st != http.StatusConflict {
		t.Fatalf("the old owner's declaration after recovery = %d, want 409", st)
	}
	// The new phone sees itself and declares: it alone is listed, and the
	// old row is gone from the hub.
	if st, body := declare(newPhone, owner.keys.KeyID()); st != http.StatusOK {
		t.Fatalf("the new phone's declaration = %d %v", st, body)
	}
	if got := ownerDevices(t, mcall, owner.tenantPath("router/inventory")); len(got) != 1 || got[0] != newPhone {
		t.Fatalf("after the new phone declared, owner devices = %v, want [%s]", got, newPhone)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_inventory_owner_devices WHERE tenant_id = $1 AND mac = $2`, owner.tenantID, oldPhone); n != 0 {
		t.Fatalf("the old phone's MAC is still stored")
	}
}
