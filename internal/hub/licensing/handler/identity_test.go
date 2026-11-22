package handler_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/handler"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

type identity struct {
	mu sync.Mutex
	n  int
}

func (i *identity) SignIn(_ context.Context, a usecase.Account) (usecase.HubSession, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	n := string(rune('0' + i.n))
	return usecase.HubSession{UserID: "u-" + string(a.ID), TenantID: "t-" + string(a.ID), AccessToken: "jwt-" + n, RefreshToken: "rt-" + n}, nil
}

func withIdentity(h *handler.Handler) { h.Svc.Identity = &identity{} }

func TestVerify_ReturnsTheHouseholdAndItsKernelTokens(t *testing.T) {
	e := newEnvWith(t, withIdentity)
	if st, body, _ := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": phone}); st != http.StatusAccepted {
		t.Fatalf("otp = %d %v", st, body)
	}
	e.sender.mu.Lock()
	c := code6.FindString(e.sender.last)
	e.sender.mu.Unlock()
	st, body, _ := e.do("POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": phone, "code": c, "device": "Pixel 7"})
	if st != http.StatusOK {
		t.Fatalf("verify = %d %v", st, body)
	}
	acc, _ := body["accountId"].(string)
	hub, _ := body["hub"].(map[string]any)
	if body["sessionToken"] == "" || hub == nil || hub["tenantId"] != "t-"+acc || hub["userId"] != "u-"+acc ||
		hub["accessToken"] != "jwt-1" || hub["refreshToken"] != "rt-1" {
		t.Fatalf("verify body %v", body)
	}
}

func TestVerify_NoHubFieldWithoutIdentity(t *testing.T) {
	e := newEnv(t)
	if st, body, _ := e.do("POST", "/licensing/v1/otp", "", map[string]any{"phone": phone}); st != http.StatusAccepted {
		t.Fatalf("otp = %d %v", st, body)
	}
	e.sender.mu.Lock()
	c := code6.FindString(e.sender.last)
	e.sender.mu.Unlock()
	_, body, _ := e.do("POST", "/licensing/v1/otp/verify", "", map[string]any{"phone": phone, "code": c})
	if _, ok := body["hub"]; ok {
		t.Fatalf("hub field on a hub without identity: %v", body)
	}
}

func TestHubSession_ReissuedWithTheLicensingSession(t *testing.T) {
	e := newEnvWith(t, withIdentity)
	tok := e.signIn() // jwt-1 / rt-1
	st, body, _ := e.do("POST", "/licensing/v1/session/hub", tok, nil)
	if st != http.StatusOK || body["accessToken"] != "jwt-2" || body["refreshToken"] != "rt-2" || body["tenantId"] == "" {
		t.Fatalf("hub session = %d %v", st, body)
	}
	if st, _, _ := e.do("POST", "/licensing/v1/session/hub", "kgs_forged", nil); st != http.StatusUnauthorized {
		t.Fatalf("forged session = %d", st)
	}
	// Signed out of the licensing session, the household is out of reach too.
	if st, _, _ := e.do("DELETE", "/licensing/v1/session", tok, nil); st != http.StatusNoContent {
		t.Fatalf("sign out = %d", st)
	}
	if st, _, _ := e.do("POST", "/licensing/v1/session/hub", tok, nil); st != http.StatusUnauthorized {
		t.Fatalf("after sign out = %d", st)
	}
}

func TestHubSession_OffWithoutIdentity(t *testing.T) {
	e := newEnv(t)
	tok := e.signIn()
	st, body, _ := e.do("POST", "/licensing/v1/session/hub", tok, nil)
	if st != http.StatusNotFound || errCode(body) != handler.CodeHubOff {
		t.Fatalf("hub session without identity = %d %v", st, body)
	}
}
