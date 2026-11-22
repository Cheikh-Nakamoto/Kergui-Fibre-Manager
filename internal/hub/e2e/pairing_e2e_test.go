package e2e

// QR pairing with the six-digit code (issue #43), the owner being the phone's
// own Go core (mobilecore): the keys it keeps in its vault, the QR it shows,
// the code it derives from what the hub lists, and the bundle it signs and
// uploads. The relays are the PC one, driven through the real
// `kergui hub enroll` command, and a phone's core.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/cli"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/mobilecore"
)

// phoneOwnerApp is the owner's app: the household session of the bench's
// owner, and the keys and signatures of a real mobilecore.
type phoneOwnerApp struct {
	*ownerApp
	core *mobilecore.Core
}

func newPhoneOwnerApp(t *testing.T, o *ownerApp) *phoneOwnerApp {
	t.Helper()
	c, err := mobilecore.Open(t.TempDir(), "e2e-vault-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &phoneOwnerApp{ownerApp: o, core: c}
}

// through is the same app talking to the hub through another server (a
// hostile proxy).
func (p *phoneOwnerApp) through(srv *httptest.Server) *phoneOwnerApp {
	o := *p.ownerApp
	o.hub = srv
	return &phoneOwnerApp{ownerApp: &o, core: p.core}
}

// currentBundle is GET trust-bundle as the app passes it to the core: ""
// when the household has none yet.
func (p *phoneOwnerApp) currentBundle() string {
	p.t.Helper()
	code, body := p.call("GET", p.tenantPath("trust-bundle"), nil)
	if code == http.StatusNotFound {
		return ""
	}
	if code != http.StatusOK {
		p.t.Fatalf("GET trust-bundle = %d %v", code, body)
	}
	b, _ := json.Marshal(body)
	return string(b)
}

// signAndUpload signs the next bundle in the core and PUTs it, returning the
// version.
func (p *phoneOwnerApp) signAndUpload(change map[string]any) uint64 {
	p.t.Helper()
	ch, _ := json.Marshal(change)
	raw, err := p.core.SignTrustBundle(p.tenantID, p.currentBundle(), string(ch))
	if err != nil {
		p.t.Fatalf("SignTrustBundle: %v", err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(raw), &body)
	if code, out := p.call("PUT", p.tenantPath("trust-bundle"), body); code != http.StatusNoContent {
		p.t.Fatalf("PUT trust-bundle = %d %v", code, out)
	}
	return uint64(body["version"].(float64))
}

// showQR is the pairing screen: a code from the hub, the core's fingerprint.
func (p *phoneOwnerApp) showQR(kind string) string {
	p.t.Helper()
	code, body := p.call("POST", p.tenantPath("agents/pairing"), map[string]any{"kind": kind})
	if code != http.StatusCreated {
		p.t.Fatalf("POST agents/pairing = %d %v", code, body)
	}
	qr, err := p.core.PairingInvite(str(body, "code"))
	if err != nil {
		p.t.Fatal(err)
	}
	return qr
}

type listedAgent struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Ed25519 string `json:"ed25519"`
	X25519  string `json:"x25519"`
}

// agent finds a relay in GET agents, as the app's screen does.
func (p *phoneOwnerApp) agent(id string) listedAgent {
	p.t.Helper()
	code, body := p.call("GET", p.tenantPath("agents"), nil)
	if code != http.StatusOK {
		p.t.Fatalf("GET agents = %d %v", code, body)
	}
	raw, _ := json.Marshal(body["agents"])
	var list []listedAgent
	_ = json.Unmarshal(raw, &list)
	for _, a := range list {
		if a.ID == id {
			return a
		}
	}
	p.t.Fatalf("relay %s is not listed", id)
	return listedAgent{}
}

// ownerCode is the six digits the owner's screen shows for a listed relay.
func (p *phoneOwnerApp) ownerCode(a listedAgent) string {
	p.t.Helper()
	code, err := p.core.PairingCode(a.Ed25519, a.X25519)
	if err != nil {
		p.t.Fatal(err)
	}
	return code
}

// confirm is "Oui, c'est le même code": the relay is signed in.
func (p *phoneOwnerApp) confirm(a listedAgent) uint64 {
	p.t.Helper()
	ed, _ := base64.StdEncoding.DecodeString(a.Ed25519)
	x, _ := base64.StdEncoding.DecodeString(a.X25519)
	return p.signAndUpload(map[string]any{"relay": map[string]any{
		"agentId": a.ID, "kind": a.Kind, "label": a.Name, "ed25519": ed, "x25519": x,
	}})
}

// pcRelay runs the real `kergui hub enroll` against its own vault and
// returns the code it printed and the state it kept.
func pcRelay(t *testing.T, hubURL, qr string) (string, agent.StateData) {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vlt := vault.New(sqlite.NewCredentialStore(db), "pc-relay-master-key")
	var out, errOut bytes.Buffer
	app := &cli.App{
		Build: func(cli.RuntimeConfig) (*cli.Services, func() error, error) {
			return &cli.Services{Vault: vlt}, nil, nil
		},
		Out: &out, Err: &errOut, Version: "e2e",
	}
	if rc := app.Run(context.Background(), []string{"hub", "enroll", "--hub", hubURL, "--name", "PC du salon", qr}); rc != 0 {
		t.Fatalf("kergui hub enroll = %d\n%s%s", rc, out.String(), errOut.String())
	}
	m := regexp.MustCompile(`Verification code: (\d{6})`).FindStringSubmatch(out.String())
	if m == nil {
		t.Fatalf("no code in the enroll output:\n%s", out.String())
	}
	blob, ok, err := vlt.LoadSecret(context.Background(), "hub-agent")
	if err != nil || !ok {
		t.Fatalf("the PC relay kept no state: %v", err)
	}
	state, err := agent.Unmarshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	return m[1], state
}

func relayTrusted(t *testing.T, state agent.StateData) agent.TrustStatus {
	t.Helper()
	_, st, err := agent.CheckTrust(context.Background(), state, http.DefaultClient)
	if err != nil {
		t.Fatalf("CheckTrust: %v", err)
	}
	return st
}

func TestE2E_QRPairingWithThePCRelayAndAPhoneRelay(t *testing.T) {
	w := newWorld(t)
	owner := newPhoneOwnerApp(t, w.owner)

	// No bundle yet: the app publishes the owner alone, so a relay has a key
	// to pin. Version 1.
	if v := owner.signAndUpload(map[string]any{"routerPin": w.boxPin}); v != 1 {
		t.Fatalf("first bundle version = %d", v)
	}

	// The PC relay: `kergui hub enroll <QR>`.
	pcCode, pcState := pcRelay(t, w.hub.URL, owner.showQR("pc"))
	pc := owner.agent(pcState.AgentID)
	if got := owner.ownerCode(pc); got != pcCode {
		t.Fatalf("owner screen %s, PC screen %s", got, pcCode)
	}
	if st := relayTrusted(t, pcState); st.Trusted {
		t.Fatal("the PC relay is trusted before the owner confirmed")
	}
	if v := owner.confirm(pc); v != 2 {
		t.Fatalf("bundle version after the PC relay = %d", v)
	}
	if st := relayTrusted(t, pcState); !st.Trusted || st.Version != 2 {
		t.Fatalf("PC relay after confirmation: %+v", st)
	}

	// A phone relay: another core scans the next QR.
	phone, err := mobilecore.Open(t.TempDir(), "relay-phone-vault", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = phone.Close() }()
	raw, err := phone.RelayEnroll(w.hub.URL, owner.showQR("dedicated"), "Téléphone du salon")
	if err != nil {
		t.Fatalf("RelayEnroll: %v", err)
	}
	var enrolled struct{ AgentID, Code string }
	_ = json.Unmarshal([]byte(raw), &enrolled)
	listed := owner.agent(enrolled.AgentID)
	if got := owner.ownerCode(listed); got != enrolled.Code {
		t.Fatalf("owner screen %s, phone relay screen %s", got, enrolled.Code)
	}
	if v := owner.confirm(listed); v != 3 {
		t.Fatalf("bundle version after the phone relay = %d", v)
	}
	var trusted struct {
		Version uint64 `json:"version"`
		Trusted bool   `json:"trusted"`
	}
	out, err := phone.RelayCheckTrust()
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(out), &trusted)
	if !trusted.Trusted || trusted.Version != 3 {
		t.Fatalf("phone relay after confirmation: %s", out)
	}
	// The PC relay is still in, and follows the newer version.
	if st := relayTrusted(t, pcState); !st.Trusted || st.Version != 3 {
		t.Fatalf("the PC relay after the phone joined: %+v", st)
	}

	// The hub refuses to go back on a version.
	stale, _ := trust.Sign(mustIdentity(t), trust.DomainBundle, trust.Bundle{TenantID: owner.tenantID, Version: 2})
	if code, _ := owner.call("PUT", owner.tenantPath("trust-bundle"), map[string]any{
		"payload": stale.Payload, "sig": stale.Sig,
	}); code != http.StatusConflict {
		t.Fatalf("PUT of an older version = %d, want 409", code)
	}
}

// swapProxy stands between the owner's app and the hub and replaces the
// relay's signing key in every agents listing: what a hub would do to slip
// its own key in.
func swapProxy(t *testing.T, hubURL string) *httptest.Server {
	t.Helper()
	target, _ := url.Parse(hubURL)
	evil := mustIdentity(t).Public()
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method != http.MethodGet || !strings.HasSuffix(resp.Request.URL.Path, "/agents") {
			return nil
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var body map[string][]map[string]any
		if err := json.Unmarshal(b, &body); err == nil {
			for _, a := range body["agents"] {
				a["ed25519"] = evil.Ed25519
				a["x25519"] = evil.X25519
			}
			b, _ = json.Marshal(body)
		}
		resp.Body = io.NopCloser(bytes.NewReader(b))
		resp.ContentLength = int64(len(b))
		resp.Header.Del("Content-Length")
		return nil
	}
	srv := httptest.NewServer(rp)
	t.Cleanup(srv.Close)
	return srv
}

func TestE2E_ASwappedKeyMakesTheCodesDifferAndPairingFails(t *testing.T) {
	w := newWorld(t)
	owner := newPhoneOwnerApp(t, w.owner)
	owner.signAndUpload(map[string]any{"routerPin": w.boxPin})

	// The relay pairs with the real hub; the owner's app reads it through
	// a hub that swaps the relay's key.
	evilOwner := owner.through(swapProxy(t, w.hub.URL))
	relayCode, state := pcRelay(t, w.hub.URL, owner.showQR("pc"))
	listed := evilOwner.agent(state.AgentID)
	ownerCode := evilOwner.ownerCode(listed)
	if ownerCode == relayCode {
		t.Fatalf("a swapped key gave the same six digits (%s)", ownerCode)
	}

	// "Non, le code est différent": the app removes the relay and signs
	// nothing.
	if code, body := owner.call("DELETE", owner.tenantPath("agents/"+state.AgentID), nil); code != http.StatusNoContent {
		t.Fatalf("DELETE agent = %d %v", code, body)
	}
	if _, _, err := agent.CheckTrust(context.Background(), state, http.DefaultClient); err == nil {
		t.Fatal("the refused relay can still read the household's trust bundle")
	}
	signed := owner.currentBundle()
	var cur struct{ Payload []byte }
	_ = json.Unmarshal([]byte(signed), &cur)
	var b trust.Bundle
	_ = json.Unmarshal(cur.Payload, &b)
	if b.Version != 1 || len(b.Relays) != 0 {
		t.Fatalf("the bundle changed after a refused pairing: v%d, %d relays", b.Version, len(b.Relays))
	}

	// The other way round: a hub that serves the relay a bundle under its
	// own owner key is caught by the fingerprint in the QR.
	hubKey := mustIdentity(t)
	forged, _ := trust.Sign(hubKey, trust.DomainBundle, trust.Bundle{TenantID: owner.tenantID, Version: 9, Owner: hubKey.Public()})
	bundleSwap := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/relay/v1/trust" {
			_ = json.NewEncoder(rw).Encode(map[string]any{"payload": forged.Payload, "sig": forged.Sig, "version": 9})
			return
		}
		target, _ := url.Parse(w.hub.URL)
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(rw, r)
	}))
	defer bundleSwap.Close()
	phone, err := mobilecore.Open(t.TempDir(), "relay-phone-vault", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = phone.Close() }()
	if _, err := phone.RelayEnroll(bundleSwap.URL, owner.showQR("dedicated"), ""); err == nil {
		t.Fatal("the phone relay accepted a bundle under the hub's own key")
	}
	if phone.RelayEnrolled() {
		t.Fatal("a refused pairing left the phone enrolled")
	}
}

func mustIdentity(t *testing.T) *trust.Identity {
	t.Helper()
	id, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
