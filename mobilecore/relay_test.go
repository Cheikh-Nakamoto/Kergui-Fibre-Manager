package mobilecore

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// fakeHub is the little of the hub pairing needs: a code to redeem, the
// relay it creates and the bundle the owner uploads. swapOwner makes it serve
// a bundle of its own instead, as a hostile hub would.
type fakeHub struct {
	mu        sync.Mutex
	bundle    signedJSON
	relayKeys trust.PublicKeys
	swapOwner bool
}

func (f *fakeHub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /relay/v1/pair/redeem", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code    string `json:"code"`
			Name    string `json:"name"`
			Ed25519 []byte `json:"ed25519"`
			X25519  []byte `json:"x25519"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Code != "CODE-1" {
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, http.StatusNotFound)
			return
		}
		f.mu.Lock()
		f.relayKeys = trust.PublicKeys{Ed25519: req.Ed25519, X25519: req.X25519}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"agentId": "A1", "tenantId": "T1", "token": "kga_test", "keyId": "x"})
	})
	mux.HandleFunc("GET /relay/v1/trust", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer kga_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		b := f.bundle
		if f.swapOwner {
			hubKey, _ := trust.Generate(rand.Reader)
			s, _ := trust.Sign(hubKey, trust.DomainBundle, trust.Bundle{TenantID: "T1", Version: 1, Owner: hubKey.Public()})
			b = signedJSON{Payload: s.Payload, Sig: s.Sig, Version: 1}
		}
		_ = json.NewEncoder(w).Encode(b)
	})
	return mux
}

func (f *fakeHub) put(t *testing.T, raw string) {
	t.Helper()
	var b signedJSON
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.bundle = b
	f.mu.Unlock()
}

func TestRelayPairing_TwoPhonesShowTheSameCodeAndTheRelayIsSignedIn(t *testing.T) {
	hub := &fakeHub{}
	srv := httptest.NewServer(hub.handler(t))
	defer srv.Close()
	owner, relay := newCore(t, nil), newCore(t, nil)

	// The owner publishes a first bundle, then shows the QR.
	first, err := owner.SignTrustBundle("T1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	hub.put(t, first)
	qr, err := owner.PairingInvite("CODE-1")
	if err != nil {
		t.Fatal(err)
	}
	if relay.RelayEnrolled() {
		t.Fatal("a fresh core says it is enrolled")
	}

	// The relay phone scans it.
	raw, err := relay.RelayEnroll(srv.URL, qr, "Téléphone du salon")
	if err != nil {
		t.Fatalf("RelayEnroll: %v", err)
	}
	var enrolled struct{ AgentID, TenantID, Code string }
	_ = json.Unmarshal([]byte(raw), &enrolled)
	if enrolled.AgentID != "A1" || enrolled.TenantID != "T1" || !relay.RelayEnrolled() {
		t.Fatalf("enrolled = %s", raw)
	}

	// The owner sees the relay's keys as the hub lists them.
	ownerCode, err := owner.PairingCode(b64(hub.relayKeys.Ed25519), b64(hub.relayKeys.X25519))
	if err != nil {
		t.Fatal(err)
	}
	if ownerCode != enrolled.Code {
		t.Fatalf("owner screen %s, relay screen %s", ownerCode, enrolled.Code)
	}
	if strings.Contains(raw, "kga_test") {
		t.Fatal("the relay token reached JS")
	}

	// Not yet trusted: the owner has not confirmed.
	if st := relayTrust(t, relay); st.Trusted || st.Version != 1 {
		t.Fatalf("before confirmation: %+v", st)
	}

	// Confirmed: the owner signs it in and uploads.
	change, _ := json.Marshal(map[string]any{"relay": map[string]any{
		"agentId": "A1", "kind": "phone", "ed25519": hub.relayKeys.Ed25519, "x25519": hub.relayKeys.X25519,
	}})
	cur, _ := json.Marshal(hub.bundle)
	second, err := owner.SignTrustBundle("T1", string(cur), string(change))
	if err != nil {
		t.Fatal(err)
	}
	hub.put(t, second)
	if st := relayTrust(t, relay); !st.Trusted || st.Version != 2 {
		t.Fatalf("after confirmation: %+v", st)
	}
}

func TestRelayPairing_AHubThatSwapsTheOwnerKeyIsRefused(t *testing.T) {
	hub := &fakeHub{swapOwner: true}
	srv := httptest.NewServer(hub.handler(t))
	defer srv.Close()
	owner, relay := newCore(t, nil), newCore(t, nil)
	qr, _ := owner.PairingInvite("CODE-1")
	if _, err := relay.RelayEnroll(srv.URL, qr, ""); err == nil {
		t.Fatal("the relay accepted a bundle signed by another owner key")
	}
	if relay.RelayEnrolled() {
		t.Fatal("a refused pairing left the relay enrolled")
	}
}

func TestRelayPairing_AMalformedInviteIsRefused(t *testing.T) {
	relay := newCore(t, nil)
	if _, err := relay.RelayEnroll("http://127.0.0.1:1", "not a QR", ""); err == nil {
		t.Fatal("enrolled with a malformed invite")
	}
	if _, err := relay.RelayCheckTrust(); err == nil {
		t.Fatal("checked trust without being enrolled")
	}
}

type relayTrustAnswer struct {
	Version uint64 `json:"version"`
	Trusted bool   `json:"trusted"`
}

func relayTrust(t *testing.T, c *Core) relayTrustAnswer {
	t.Helper()
	raw, err := c.RelayCheckTrust()
	if err != nil {
		t.Fatalf("RelayCheckTrust: %v", err)
	}
	var st relayTrustAnswer
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	return st
}
