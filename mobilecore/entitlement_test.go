package mobilecore

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// The serial the F6600P mock reports, the identity a pass binds to.
const mockSerial = "ZTEG00000001"

var gateT0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

const gateDay = 24 * time.Hour

// fakeClock is the core's clock in these tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func gateKey(b byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
}

// testKey is the key the core trusts in these tests; otherKey it does not.
var (
	testPub, testPriv = gateKey(7)
	_, otherPriv      = gateKey(8)
)

func signToken(t *testing.T, priv ed25519.PrivateKey, tier entitlement.Tier, box string, iat time.Time) string {
	t.Helper()
	tok, err := entitlement.Sign(priv, entitlement.Claims{
		Account: "acc-1", BoxID: box, Tier: tier, IssuedAt: iat, ExpiresAt: iat.Add(10 * gateDay),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// gatedCore is a core pointed at the F6600P mock (which reports a serial),
// logged in, trusting testPub, on a fake clock at gateT0.
func gatedCore(t *testing.T) (*Core, *fakeClock, *mockrouter.F6600P) {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "s3cret")
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	c := newCore(t, nil)
	clk := &fakeClock{t: gateT0}
	c.gate.now = clk.now
	if err := c.Configure(srv.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"s3cret"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	if err := c.SetPublicKeys(base64.StdEncoding.EncodeToString(testPub)); err != nil {
		t.Fatal(err)
	}
	return c, clk, box
}

// aDevice is a device of the mock box that is not blocked.
func aDevice(t *testing.T, c *Core) string {
	t.Helper()
	for mac, d := range devices(t, c) {
		if !d.Blocked {
			return mac
		}
	}
	t.Fatal("no unblocked device on the mock box")
	return ""
}

type action struct {
	name, method, path, body string
	tier                     entitlement.Tier // the lowest that unlocks it
}

func paidActions(mac string) []action {
	return []action{
		{"block", "POST", "/api/devices/" + mac + "/block", "", entitlement.TierBasic},
		{"cut Wi-Fi access", "POST", "/api/devices/" + mac + "/wifi-block", "", entitlement.TierBasic},
		{"MAC filter on", "POST", "/api/filter/toggle", `{"enabled":true}`, entitlement.TierBasic},
		{"SSID off", "PATCH", "/api/wifi/ssids/1", `{"enabled":false}`, entitlement.TierMax},
		{"SSID rename", "PATCH", "/api/wifi/ssids/1", `{"name":"Maison"}`, entitlement.TierMax},
		{"Wi-Fi password", "PATCH", "/api/wifi/ssids/1", `{"password":"12345678"}`, entitlement.TierMax},
	}
}

func giveBackActions(mac string) []action {
	return []action{
		{"unblock", "POST", "/api/devices/" + mac + "/unblock", "", entitlement.TierFree},
		{"restore Wi-Fi access", "POST", "/api/devices/" + mac + "/wifi-unblock", "", entitlement.TierFree},
		{"MAC filter off", "POST", "/api/filter/toggle", `{"enabled":false}`, entitlement.TierFree},
		{"SSID on", "PATCH", "/api/wifi/ssids/1", `{"enabled":true}`, entitlement.TierFree},
	}
}

// refused reports whether the answer is the core's not-entitled refusal.
func refused(t *testing.T, status int, body string) bool {
	t.Helper()
	var out map[string]string
	_ = json.Unmarshal([]byte(body), &out)
	isCode := out["code"] == "not_entitled"
	if isCode != (status == http.StatusForbidden) {
		t.Fatalf("status %d with body %s: the 403 and the code must go together", status, body)
	}
	return isCode
}

// TestGateByTier is the acceptance table of #24 against the mock router on a
// fake clock: no token, Basic, Max, expired within the 3-day grace, past it.
func TestGateByTier(t *testing.T) {
	basic := signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0)
	maxTok := signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0)
	exp := gateT0.Add(10 * gateDay)

	for _, tc := range []struct {
		name  string
		token string
		now   time.Time
		tier  entitlement.Tier // what it unlocks
	}{
		{"no token", "", gateT0, entitlement.TierFree},
		{"basic", basic, gateT0.Add(gateDay), entitlement.TierBasic},
		{"max", maxTok, gateT0.Add(gateDay), entitlement.TierMax},
		{"max, expired within the grace", maxTok, exp.Add(entitlement.OfflineGrace - time.Second), entitlement.TierMax},
		{"basic, expired within the grace", basic, exp.Add(gateDay), entitlement.TierBasic},
		{"max, past the grace", maxTok, exp.Add(entitlement.OfflineGrace), entitlement.TierFree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, clk, _ := gatedCore(t)
			clk.set(tc.now)
			c.SetEntitlement(tc.token)
			mac := aDevice(t, c)
			for _, a := range paidActions(mac) {
				status, body := call(t, c, a.method, a.path, a.body)
				if want := !tc.tier.Includes(a.tier); refused(t, status, body) != want {
					t.Errorf("%s: refused = %v, want %v (%d %s)", a.name, !want, want, status, body)
				}
			}
			// Whatever the token, giving access back goes through.
			for _, a := range giveBackActions(mac) {
				if status, body := call(t, c, a.method, a.path, a.body); refused(t, status, body) {
					t.Errorf("%s refused: %d %s", a.name, status, body)
				}
			}
		})
	}
}

// TestGateBlockReachesTheBox: an entitled block really blocks, a refused one
// never reaches the box, and unblocking needs no token.
func TestGateBlockReachesTheBox(t *testing.T) {
	c, _, _ := gatedCore(t)
	mac := aDevice(t, c)

	status, body := call(t, c, "POST", "/api/devices/"+mac+"/block", "")
	if status != http.StatusForbidden || !strings.Contains(body, `"required": "basic"`) || !strings.Contains(body, `"feature": "block"`) {
		t.Fatalf("block without a token = %d %s", status, body)
	}
	if devices(t, c)[mac].Blocked {
		t.Fatal("a refused block reached the box")
	}

	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	if status, body := call(t, c, "POST", "/api/devices/"+mac+"/block", ""); status != http.StatusOK {
		t.Fatalf("block with Basic = %d %s", status, body)
	}
	if !devices(t, c)[mac].Blocked {
		t.Fatal("device not blocked")
	}

	// The pass is gone (signed out, or another phone): unblock still works.
	c.SetEntitlement("")
	if status, body := call(t, c, "POST", "/api/devices/"+mac+"/unblock", ""); status != http.StatusOK {
		t.Fatalf("unblock without a token = %d %s", status, body)
	}
	if devices(t, c)[mac].Blocked {
		t.Fatal("device still blocked")
	}
}

// TestGateRejectsBadTokens: a bad signature, a key id the core does not
// trust, or a token for another box serial unlock nothing.
func TestGateRejectsBadTokens(t *testing.T) {
	good := signToken(t, testPriv, entitlement.TierMax, mockSerial, gateT0)
	p, s, _ := strings.Cut(good, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(s)
	sig[0] ^= 0xff
	tampered := p + "." + base64.RawURLEncoding.EncodeToString(sig)

	for _, tc := range []struct{ name, token string }{
		{"bad signature", tampered},
		{"unknown key id", signToken(t, otherPriv, entitlement.TierMax, mockSerial, gateT0)},
		{"another box serial", signToken(t, testPriv, entitlement.TierMax, "ZTEG99999999", gateT0)},
		{"garbage", "not-a-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := gatedCore(t)
			c.SetEntitlement(tc.token)
			mac := aDevice(t, c)
			for _, a := range paidActions(mac) {
				if status, body := call(t, c, a.method, a.path, a.body); !refused(t, status, body) {
					t.Errorf("%s allowed: %d %s", a.name, status, body)
				}
			}
			for _, a := range giveBackActions(mac) {
				if status, body := call(t, c, a.method, a.path, a.body); refused(t, status, body) {
					t.Errorf("%s refused: %d %s", a.name, status, body)
				}
			}
		})
	}
}

// TestGateKeys: no trusted key unlocks nothing; an invalid key list is
// refused and trusts nothing; several keys are trusted by key id.
func TestGateKeys(t *testing.T) {
	c, _, _ := gatedCore(t)
	tok := signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0)
	c.SetEntitlement(tok)
	mac := aDevice(t, c)
	block := func() (int, string) { return call(t, c, "POST", "/api/devices/"+mac+"/block", "") }

	if err := c.SetPublicKeys("not a key"); err == nil {
		t.Fatal("an invalid key list was accepted")
	}
	if status, body := block(); !refused(t, status, body) {
		t.Fatalf("block after an invalid key list = %d %s", status, body)
	}
	otherPub := otherPriv.Public().(ed25519.PublicKey)
	enc := base64.StdEncoding.EncodeToString
	if err := c.SetPublicKeys(enc(otherPub) + "," + enc(testPub)); err != nil {
		t.Fatal(err)
	}
	if status, body := block(); status != http.StatusOK {
		t.Fatalf("block with two trusted keys = %d %s", status, body)
	}
}

// TestGateReadsTheSerialOfTheNewBox: pointing the core at another box drops
// the serial read for the previous one.
func TestGateReadsTheSerialOfTheNewBox(t *testing.T) {
	c, _, _ := gatedCore(t)
	c.SetEntitlement(signToken(t, testPriv, entitlement.TierBasic, mockSerial, gateT0))
	mac := aDevice(t, c)
	if status, body := call(t, c, "POST", "/api/devices/"+mac+"/wifi-block", ""); status != http.StatusOK {
		t.Fatalf("wifi-block = %d %s", status, body)
	}
	// An F660, whose serial cannot be read: no pass binds to it.
	f660 := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	t.Cleanup(f660.Close)
	if err := c.Configure(f660.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"admin"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	if status, body := call(t, c, "POST", "/api/devices/ac:bb:cc:00:00:22/block", ""); !refused(t, status, body) {
		t.Fatalf("block on a box without a readable serial = %d %s", status, body)
	}
	if status, body := call(t, c, "POST", "/api/devices/ac:bb:cc:00:00:22/unblock", ""); status != http.StatusOK {
		t.Fatalf("unblock = %d %s", status, body)
	}
}

// TestGateDemoOpensEverything: the demo box needs no token (store
// reviewers see every tier), as in the app.
func TestGateDemoOpensEverything(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"admin"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	for _, a := range paidActions("ac:bb:cc:00:00:22") {
		if status, body := call(t, c, a.method, a.path, a.body); refused(t, status, body) {
			t.Errorf("demo %s refused: %d %s", a.name, status, body)
		}
	}
}
