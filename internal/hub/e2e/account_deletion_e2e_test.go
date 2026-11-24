package e2e

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The app's « Supprimer mon compte » (#37, Google Play's account-deletion
// policy) end to end: DELETE /licensing/v1/account with the kgs_ session
// erases the account and everything the hub keeps for it, in one
// transaction; a household is erased only when nobody else is in it.

// deletionIP gives each licensing call its own address (the routes share a
// budget per address).
var deletionIP = 0

func deletionCall(t *testing.T, w *world, method, path, token string, in any) (int, map[string]any) {
	t.Helper()
	deletionIP++
	return callFrom(t, w.hub.URL, fmt.Sprintf("198.51.100.%d", 60+deletionIP%60), method, path, token, in)
}

// accountOf is the licensing account behind a household's owner.
func accountOf(t *testing.T, tenantID string) string {
	t.Helper()
	return str1(t, `SELECT account_id::text FROM hub_identity_households WHERE tenant_id = $1`, tenantID)
}

// tenantTables is every table of the base with a tenant_id column.
func tenantTables(t *testing.T) []string {
	t.Helper()
	rows, err := e2ePool(t).Query(context.Background(), `SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'tenant_id' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// rowsOfTenant counts, table by table, the rows a household still has.
func rowsOfTenant(t *testing.T, tenantID string) map[string]int {
	t.Helper()
	left := map[string]int{}
	for _, table := range tenantTables(t) {
		q := `SELECT count(*) FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE tenant_id = $1`
		if n := countRows(t, q, tenantID); n > 0 {
			left[table] = n
		}
	}
	if n := countRows(t, `SELECT count(*) FROM tenants WHERE id = $1`, tenantID); n > 0 {
		left["tenants"] = n
	}
	return left
}

// accountRows counts what the hub still keeps for an account, its number
// and its kernel user.
func accountRows(t *testing.T, accountID, phone, userID string) map[string]int {
	t.Helper()
	left := map[string]int{}
	for name, q := range map[string]string{
		"account":       `SELECT count(*) FROM hub_licensing_accounts WHERE id::text = $1`,
		"sessions":      `SELECT count(*) FROM hub_licensing_sessions WHERE account_id::text = $1`,
		"subscriptions": `SELECT count(*) FROM hub_licensing_subscriptions WHERE account_id::text = $1`,
		"payments":      `SELECT count(*) FROM hub_licensing_payments WHERE account_id::text = $1`,
		"household":     `SELECT count(*) FROM hub_identity_households WHERE account_id::text = $1`,
	} {
		if n := countRows(t, q, accountID); n > 0 {
			left[name] = n
		}
	}
	for name, q := range map[string]string{
		"trials":      `SELECT count(*) FROM hub_licensing_trials WHERE phone = $1`,
		"codes":       `SELECT count(*) FROM hub_licensing_otp_challenges WHERE phone = $1`,
		"complaints":  `SELECT count(*) FROM hub_complaints WHERE phone = $1`,
		"invites":     `SELECT count(*) FROM hub_identity_invites WHERE phone = $1`,
		"optins":      `SELECT count(*) FROM hub_whatsapp_optins WHERE phone = $1`,
		"queue":       `SELECT count(*) FROM hub_whatsapp_queue WHERE phone = $1`,
		"complaintMs": `SELECT count(*) FROM hub_complaint_messages m JOIN hub_complaints c ON c.id = m.complaint_id WHERE c.phone = $1`,
	} {
		if n := countRows(t, q, phone); n > 0 {
			left[name] = n
		}
	}
	for name, q := range map[string]string{
		"user":           `SELECT count(*) FROM users WHERE id::text = $1`,
		"memberships":    `SELECT count(*) FROM memberships WHERE user_id::text = $1`,
		"refresh tokens": `SELECT count(*) FROM refresh_tokens WHERE user_id::text = $1`,
		"push tokens":    `SELECT count(*) FROM hub_push_tokens WHERE user_id::text = $1`,
	} {
		if n := countRows(t, q, userID); n > 0 {
			left[name] = n
		}
	}
	return left
}

// fillAccount gives a phone owner what a real customer has on the hub: an
// owner phone, a backup, a push token, a complaint, a trial on a box and a
// credited payment.
func fillAccount(t *testing.T, w *world, o *ownerApp, number, box string) {
	t.Helper()
	if st, body := o.call(http.MethodPut, o.tenantPath("router/owner-devices"),
		map[string]any{"mac": "fa:4b:47:00:00:0" + box[len(box)-1:], "ownerKey": o.keys.KeyID()}); st != http.StatusOK {
		t.Fatalf("declare the owner's phone = %d %v", st, body)
	}
	archive := backupArchive(t, []backup.Device{{MAC: "aa:bb:cc:00:00:01", Name: "Télé", FirstSeen: time.Now().Unix()}}, nil)
	for range 2 { // the second save keeps the first as a version
		if st, body := o.call(http.MethodPut, o.tenantPath("router/backups/192-168-1-1"),
			map[string]any{"archive": base64.StdEncoding.EncodeToString(archive)}); st != http.StatusOK {
			t.Fatalf("backup = %d %v", st, body)
		}
	}
	if st, body := o.call(http.MethodPut, o.tenantPath("push-tokens"),
		map[string]any{"token": "ExponentPushToken[del-" + box + "]", "platform": "android"}); st != http.StatusNoContent {
		t.Fatalf("push token = %d %v", st, body)
	}
	if st, body := deletionCall(t, w, http.MethodPost, "/licensing/v1/complaints", o.session,
		map[string]any{"category": "box", "message": "La box redémarre la nuit."}); st != http.StatusCreated {
		t.Fatalf("complaint = %d %v", st, body)
	}
	if st, body := deletionCall(t, w, http.MethodPost, "/licensing/v1/trial", o.session, nil); st != http.StatusOK {
		t.Fatalf("trial = %d %v", st, body)
	}
	if st, body := deletionCall(t, w, http.MethodPost, "/licensing/v1/bind", o.session, map[string]any{"boxId": box}); st != http.StatusOK {
		t.Fatalf("bind = %d %v", st, body)
	}
	if _, err := e2ePool(t).Exec(context.Background(), `INSERT INTO hub_licensing_payments
		(id, provider, provider_ref, account_id, box_id, pass_code, amount_xof, status, created_at, credited_at)
		VALUES ($1, 'support', $2, $3, $4, 'max_1m', 0, 'credited', now(), now())`,
		uuid.NewString(), "del-"+box, accountOf(t, o.tenantID), box); err != nil {
		t.Fatal(err)
	}
	if left := accountRows(t, accountOf(t, o.tenantID), number, o.userID); left["payments"] == 0 || left["trials"] == 0 ||
		left["complaints"] == 0 || left["push tokens"] == 0 || left["subscriptions"] == 0 {
		t.Fatalf("the account is not filled: %v", left)
	}
}

// waitForDeletionAudit waits for the audit row the deletion writes through
// the kernel's outbox.
func waitForDeletionAudit(t *testing.T, accountID string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var meta string
		err := e2ePool(t).QueryRow(context.Background(), `SELECT metadata::text FROM audit_logs
			WHERE action = 'ACCOUNT_DELETED' AND target_id::text = $1`, accountID).Scan(&meta)
		if err == nil {
			return meta
		}
		if time.Now().After(deadline) {
			t.Fatalf("no ACCOUNT_DELETED audit row for %s: %v", accountID, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestE2E_DeleteAccountErasesTheSoleOwnersHousehold: an owner alone in the
// household deletes the account from the app; the account, its sessions,
// rights, trial, payments, complaints, kernel user and the whole household
// (owner phones, backups and their versions, relays…) are gone, the
// sessions no longer open, and another customer keeps everything.
func TestE2E_DeleteAccountErasesTheSoleOwnersHousehold(t *testing.T) {
	ownerNumber := "+22176" + randomDigits(t, 7)
	strangerNumber := "+22177" + randomDigits(t, 7)
	w := newWorldWith(t, fastTimings, withPhoneOwner(ownerNumber))
	owner := w.owner
	owner.registerNetwork(w.boxPin)
	owner.publishBundle(w.boxPin)
	fillAccount(t, w, owner, ownerNumber, "ZTEG"+randomDigits(t, 8))
	accountID := accountOf(t, owner.tenantID)

	// Another customer, with a household of its own.
	s := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.50", strangerNumber, "stranger")
	keys, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	stranger := &ownerApp{t: t, hub: w.hub, keys: keys, token: s.access, tenantID: s.tenantID, userID: s.userID, session: s.session}
	stranger.registerNetwork(w.boxPin)
	stranger.publishBundle(w.boxPin)
	fillAccount(t, w, stranger, strangerNumber, "ZTEG"+randomDigits(t, 8))
	strangerHousehold := rowsOfTenant(t, stranger.tenantID)
	strangerAccount := accountRows(t, s.accountID, strangerNumber, s.userID)

	// Anonymous: refused.
	if st, _ := deletionCall(t, w, http.MethodDelete, "/licensing/v1/account", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("anonymous delete = %d, want 401", st)
	}
	if st, body := deletionCall(t, w, http.MethodDelete, "/licensing/v1/account", owner.session, nil); st != http.StatusNoContent {
		t.Fatalf("delete = %d %v", st, body)
	}

	if left := accountRows(t, accountID, ownerNumber, owner.userID); len(left) != 0 {
		t.Fatalf("the hub kept rows of the deleted account: %v", left)
	}
	if left := rowsOfTenant(t, owner.tenantID); len(left) != 0 {
		t.Fatalf("the hub kept rows of the deleted household: %v", left)
	}
	// The audit events still in the outbox (the sign-in's) no longer name the
	// erased household or user: audit_logs would refuse them for ever.
	if n := countRows(t, `SELECT count(*) FROM event_outbox WHERE published_at IS NULL
		AND (payload->>'tenant_id' = $1 OR payload->>'actor_id' = $2)`, owner.tenantID, owner.userID); n != 0 {
		t.Fatalf("%d outbox events still name the erased household or user", n)
	}

	// The sessions are dead: licensing, kernel access and refresh.
	if st, _ := deletionCall(t, w, http.MethodGet, "/licensing/v1/subscriptions", owner.session, nil); st != http.StatusUnauthorized {
		t.Fatalf("the licensing session after the deletion = %d, want 401", st)
	}
	if st, _ := owner.call(http.MethodGet, owner.tenantPath("router/backups"), nil); st == http.StatusOK {
		t.Fatal("the kernel access token still reads the household")
	}
	if st, _ := deletionCall(t, w, http.MethodPost, "/api/v1/auth/refresh", "", map[string]any{"refreshToken": owner.refreshToken}); st == http.StatusOK {
		t.Fatal("the refresh token still works")
	}
	if st, _ := deletionCall(t, w, http.MethodDelete, "/licensing/v1/account", owner.session, nil); st != http.StatusUnauthorized {
		t.Fatalf("deleting again = %d, want 401", st)
	}

	// The other customer is untouched, and still works.
	if got := rowsOfTenant(t, stranger.tenantID); fmt.Sprint(got) != fmt.Sprint(strangerHousehold) {
		t.Fatalf("the other household changed: %v, was %v", got, strangerHousehold)
	}
	if got := accountRows(t, s.accountID, strangerNumber, s.userID); fmt.Sprint(got) != fmt.Sprint(strangerAccount) {
		t.Fatalf("the other account changed: %v, was %v", got, strangerAccount)
	}
	if st, _ := deletionCall(t, w, http.MethodGet, "/licensing/v1/subscriptions", s.session, nil); st != http.StatusOK {
		t.Fatalf("the other customer's session = %d", st)
	}
	if st, _ := stranger.call(http.MethodGet, stranger.tenantPath("router/backups/192-168-1-1"), nil); st != http.StatusOK {
		t.Fatalf("the other customer's backup = %d", st)
	}

	// The action log says it, without the number.
	meta := waitForDeletionAudit(t, accountID)
	if strings.Contains(meta, ownerNumber) || !strings.Contains(meta, `"households_erased": 1`) {
		t.Fatalf("audit metadata = %s", meta)
	}
}

// TestE2E_DeleteAccountLeavesASharedHouseholdToItsMembers: the owner of a
// household with a member deletes the account. The household stays for the
// member (its backups too); only the owner leaves it, with the owner's
// phones. When the member deletes theirs in turn, nobody is left: the
// household goes then.
func TestE2E_DeleteAccountLeavesASharedHouseholdToItsMembers(t *testing.T) {
	ownerNumber := "+22176" + randomDigits(t, 7)
	memberNumber := "+22175" + randomDigits(t, 7)
	w := newWorldWith(t, fastTimings, withPhoneOwner(ownerNumber))
	owner := w.owner
	owner.registerNetwork(w.boxPin)
	owner.publishBundle(w.boxPin)
	fillAccount(t, w, owner, ownerNumber, "ZTEG"+randomDigits(t, 8))
	accountID := accountOf(t, owner.tenantID)

	if st, body := owner.call(http.MethodPost, membersPath(owner.tenantID, "invites"), map[string]any{"phone": memberNumber, "role": "viewer"}); st != http.StatusCreated {
		t.Fatalf("invite = %d %v", st, body)
	}
	m := otpSignIn(t, w.hub.URL, w.notifier, "198.51.100.51", memberNumber, "Tecno Spark")
	if m.tenantID != owner.tenantID {
		t.Fatalf("the member signed in to %s, want %s", m.tenantID, owner.tenantID)
	}
	memberHome := str1(t, `SELECT tenant_id::text FROM hub_identity_households WHERE account_id::text = $1`, m.accountID)

	if st, body := deletionCall(t, w, http.MethodDelete, "/licensing/v1/account", owner.session, nil); st != http.StatusNoContent {
		t.Fatalf("the owner's delete = %d %v", st, body)
	}
	if left := accountRows(t, accountID, ownerNumber, owner.userID); len(left) != 0 {
		t.Fatalf("the hub kept rows of the deleted owner: %v", left)
	}
	kept := rowsOfTenant(t, owner.tenantID)
	if kept["tenants"] != 1 || kept["hub_backups"] != 1 || kept["memberships"] != 1 {
		t.Fatalf("the shared household was not left to its member: %v", kept)
	}
	if kept["hub_inventory_owner_devices"] != 0 {
		t.Fatalf("the deleted owner's phones are still listed: %v", kept)
	}
	// The member still reads the household's backup.
	if st, body := callFrom(t, w.hub.URL, "198.51.100.51", http.MethodGet,
		owner.tenantPath("router/backups/192-168-1-1"), m.access, nil); st != http.StatusOK {
		t.Fatalf("the member's backup after the owner left = %d %v", st, body)
	}
	if meta := waitForDeletionAudit(t, accountID); !strings.Contains(meta, `"households_left": 1`) {
		t.Fatalf("audit metadata = %s", meta)
	}

	// The member deletes theirs: nobody is left in either household.
	if st, body := deletionCall(t, w, http.MethodDelete, "/licensing/v1/account", m.session, nil); st != http.StatusNoContent {
		t.Fatalf("the member's delete = %d %v", st, body)
	}
	for _, tenant := range []string{owner.tenantID, memberHome} {
		if left := rowsOfTenant(t, tenant); len(left) != 0 {
			t.Fatalf("household %s kept rows once empty: %v", tenant, left)
		}
	}
	if left := accountRows(t, m.accountID, memberNumber, m.userID); len(left) != 0 {
		t.Fatalf("the hub kept rows of the deleted member: %v", left)
	}
}
