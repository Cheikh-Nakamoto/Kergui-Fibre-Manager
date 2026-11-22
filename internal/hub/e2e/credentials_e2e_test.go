package e2e

// The sealed box password (issue #44): the owner's phone core seals the
// password it logged in to the box with, for the relay it signed in; the
// hub stores ciphertext; the relay fetches it, checks the owner sealed it,
// and logs in to the mock F6600P. A new password reaches it through
// credential_version, and a blob the owner did not seal is refused.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// syncBuffer is a log sink several goroutines write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// loginToBox is the app's login screen: the core logs in and keeps the
// password in its vault.
func (p *phoneOwnerApp) loginToBox(pass string) {
	p.t.Helper()
	body, _ := json.Marshal(map[string]string{"username": boxUser, "password": pass})
	res, err := p.core.Request("POST", "/api/login", string(body))
	if err != nil {
		p.t.Fatal(err)
	}
	if res.Status != http.StatusOK {
		p.t.Fatalf("login to the box = %d %s", res.Status, res.Body)
	}
}

// uploadSealed is what the app does after a login or a pairing: the core
// seals the password for every relay of the bundle, and each blob is PUT.
func (p *phoneOwnerApp) uploadSealed() uint64 {
	p.t.Helper()
	raw, err := p.core.SealCredentials(p.tenantID, p.currentBundle(), "")
	if err != nil {
		p.t.Fatalf("SealCredentials: %v", err)
	}
	var out struct {
		Version     uint64 `json:"version"`
		BoxPassword bool   `json:"boxPassword"`
		Relays      []struct {
			AgentID string `json:"agentId"`
			Blob    []byte `json:"blob"`
			Version uint64 `json:"version"`
		} `json:"relays"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		p.t.Fatal(err)
	}
	if !out.BoxPassword || len(out.Relays) == 0 {
		p.t.Fatalf("nothing was sealed: %s", raw)
	}
	for _, r := range out.Relays {
		code, body := p.call("PUT", p.tenantPath("agents/"+r.AgentID+"/credentials"), map[string]any{"blob": r.Blob, "version": r.Version})
		// 409: this version is already there, the password did not change.
		if code != http.StatusNoContent && code != http.StatusConflict {
			p.t.Fatalf("PUT credentials = %d %v", code, body)
		}
	}
	return out.Version
}

// storedCredentialVersion is what the hub keeps for the relay.
func storedCredentialVersion(t *testing.T, tenantID, agentID string) int64 {
	t.Helper()
	var v int64
	if err := e2ePool(t).QueryRow(context.Background(),
		`SELECT version FROM hub_sealed_credentials WHERE tenant_id = $1 AND agent_id = $2`,
		tenantID, agentID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// databaseRowsContaining lists the rows of every table of the hub's schema
// whose text form contains needle: the password must be in none.
func databaseRowsContaining(t *testing.T, needle string) []string {
	t.Helper()
	ctx := context.Background()
	pool := e2ePool(t)
	rows, err := pool.Query(ctx, `SELECT table_schema, table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('pg_catalog', 'information_schema')`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, fmt.Sprintf("%q.%q", schema, name))
	}
	rows.Close()
	var found []string
	for _, tbl := range tables {
		// The blob is bytea: its text form is hex, so the password is
		// also looked for as hex.
		var n int
		q := `SELECT count(*) FROM ` + tbl + ` t WHERE t::text ILIKE '%' || $1 || '%' OR t::text ILIKE '%' || $2 || '%'`
		if err := pool.QueryRow(ctx, q, needle, fmt.Sprintf("%x", needle)).Scan(&n); err != nil {
			t.Fatalf("%s: %v", tbl, err)
		}
		if n > 0 {
			found = append(found, tbl)
		}
	}
	return found
}

// waitForLoginsAbove waits for the relay to log in to the box again.
func waitForLoginsAbove(t *testing.T, w *world, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if w.box.Logins() > n {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s: the relay never logged in to the box again (%d logins)", what, w.box.Logins())
}

func TestE2E_SealedBoxPasswordFromThePhoneToTheRelay(t *testing.T) {
	hubLog := &syncBuffer{}
	w := newWorldWith(t, fastTimings, withHubLog(hubLog))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A password no other run of the shared database has seen.
	first := fmt.Sprintf("Box-Pass-%d", time.Now().UnixNano())
	w.box.SetPassword(first)

	// The owner's phone logs in to the box: the core keeps the password.
	owner := newPhoneOwnerApp(t, w.owner)
	if err := owner.core.Configure(w.boxURL, "zte_f6600p", w.boxPin); err != nil {
		t.Fatal(err)
	}
	owner.loginToBox(first)

	// Pairing (#43): bundle with the owner alone, QR, the relay redeems,
	// same six digits, the relay is signed in.
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

	// The relay runs, as `kergui serve --hub` does, with nothing but the
	// hub to learn the password from.
	relayLog := &syncBuffer{}
	handler, vlt := newRouterAPI(t, w.boxURL, w.boxPin)
	ag, err := agent.New(agent.Config{
		HubURL:  w.hub.URL,
		Handler: handler,
		State:   &agent.MemoryState{Data: state},
		Router: func(ctx context.Context) (string, error) {
			return httpkit.ProbeFingerprint(ctx, w.boxURL, 5*time.Second)
		},
		WiFi:    func(context.Context) (string, string) { return "MonWiFi-2G", "" },
		LANIP:   "192.168.1.50",
		Version: "e2e",
		Credentials: func(ctx context.Context, c domain.Credentials) error {
			return vlt.Store(ctx, usecase.RouterID(w.boxURL), c)
		},
		Log:     logging.New(relayLog, true),
		Timings: w.timings,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ag.Run(ctx) }()

	// Seal → hub → relay fetch → login on the mock.
	if v := owner.uploadSealed(); v != 1 {
		t.Fatalf("first password sealed at v%d", v)
	}
	before := w.box.Logins()
	waitForDevices(t, w)
	waitForLoginsAbove(t, w, before, "first password")

	// Sealing again with the same password moves nothing.
	if v := owner.uploadSealed(); v != 1 {
		t.Fatalf("the same password sealed at v%d", v)
	}

	// The household changes the box password; the owner logs in with the
	// new one (which also ends the relay's session) and the app re-seals.
	second := first + "-nouveau"
	w.box.SetPassword(second)
	owner.loginToBox(second)
	afterOwner := w.box.Logins()
	if v := owner.uploadSealed(); v != 2 {
		t.Fatalf("the new password sealed at v%d, want 2", v)
	}
	if v := storedCredentialVersion(t, w.owner.tenantID, res.AgentID); v != 2 {
		t.Fatalf("the hub keeps credentials v%d", v)
	}
	// The relay learns it from credential_version and logs in with it: with
	// the old one it could not.
	waitForLoginsAbove(t, w, afterOwner, "changed password")

	// A blob the owner did not seal — the hub's own, say, with a password of
	// its choosing — is refused: the relay keeps the owner's.
	rogue, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged, err := trust.SealCredentials(rand.Reader, rogue, relayPublic(t, state).X25519, trust.Credentials{
		TenantID: w.owner.tenantID, AgentID: res.AgentID, Username: boxUser, Password: "hub-knows-best", Version: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, body := w.owner.call("PUT", w.owner.tenantPath("agents/"+res.AgentID+"/credentials"), map[string]any{
		"blob": forged, "version": 3,
	}); code != http.StatusNoContent {
		t.Fatalf("PUT forged credentials = %d %v", code, body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(relayLog.String(), "refusing the sealed credentials") {
		if time.Now().After(deadline) {
			t.Fatal("the relay did not refuse the forged blob")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The relay's session ends (the box is restarted, say): it logs back
	// in with the owner's password, not the forged one.
	n := w.box.Logins()
	w.box.SetPassword(second)
	defer func() {
		if t.Failed() {
			t.Logf("relay log:\n%s", relayLog.String())
		}
	}()
	waitForLoginsAbove(t, w, n, "after the forged blob")

	// The hub stores only ciphertext: neither password is anywhere in its
	// database, nor in its logs, nor in the relay's. (The search does find
	// what is there: the household's id.)
	if len(databaseRowsContaining(t, w.owner.tenantID)) == 0 {
		t.Fatal("the database search finds nothing at all")
	}
	for _, pass := range []string{first, second} {
		if tables := databaseRowsContaining(t, pass); len(tables) > 0 {
			t.Errorf("the box password is in the hub's database: %v", tables)
		}
		if strings.Contains(hubLog.String(), pass) {
			t.Error("the box password is in the hub's log")
		}
		if strings.Contains(relayLog.String(), pass) {
			t.Error("the box password is in the relay's log")
		}
	}
	if hubLog.String() == "" {
		t.Error("the hub logged nothing: the log check above proves nothing")
	}
}
