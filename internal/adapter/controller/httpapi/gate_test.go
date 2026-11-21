package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// freeOnly is a gate with no token: what the tier table makes free passes,
// the rest is refused. It records what it was asked.
type freeOnly struct {
	mu    sync.Mutex
	asked []entitlement.Feature
}

func (g *freeOnly) Check(_ context.Context, f entitlement.Feature) error {
	g.mu.Lock()
	g.asked = append(g.asked, f)
	g.mu.Unlock()
	return entitlement.Authorize(entitlement.KeySet{}, "", "", f, time.Now())
}

func send(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestAPI_GateRefusesPaidWrites: with a gate set, a paid write answers 403
// with the stable code the app maps, names the feature and its tier, and
// never reaches the box; the give-back writes go through.
func TestAPI_GateRefusesPaidWrites(t *testing.T) {
	gate := &freeOnly{}
	box := mockrouter.New("admin", "admin")
	api := newAPIGated(t, box.Handler(), domain.Credentials{Username: "admin", Password: "admin"}, gate)
	const mac = "ac:bb:cc:00:00:11"

	for _, tc := range []struct {
		method, path, body string
		feature            entitlement.Feature
		required           entitlement.Tier
	}{
		{"POST", "/api/devices/" + mac + "/block", "", entitlement.FeatureBlock, entitlement.TierBasic},
		{"POST", "/api/devices/" + mac + "/wifi-block", "", entitlement.FeatureCutWiFiAccess, entitlement.TierBasic},
		{"POST", "/api/filter/toggle", `{"enabled":true}`, entitlement.FeatureCutWiFiAccess, entitlement.TierBasic},
		{"PATCH", "/api/wifi/ssids/1", `{"enabled":false}`, entitlement.FeatureSSIDManage, entitlement.TierMax},
		{"PATCH", "/api/wifi/ssids/1", `{"name":"Maison"}`, entitlement.FeatureSSIDManage, entitlement.TierMax},
		{"PATCH", "/api/wifi/ssids/1", `{"hidden":true}`, entitlement.FeatureSSIDManage, entitlement.TierMax},
		{"PATCH", "/api/wifi/ssids/1", `{"password":"12345678"}`, entitlement.FeatureWiFiPassword, entitlement.TierMax},
	} {
		code, body := send(t, tc.method, api.URL+tc.path, tc.body)
		if code != http.StatusForbidden || body["code"] != "not_entitled" ||
			body["feature"] != string(tc.feature) || body["required"] != string(tc.required) {
			t.Errorf("%s %s %s = %d %v; want 403 not_entitled %s/%s", tc.method, tc.path, tc.body, code, body, tc.feature, tc.required)
		}
	}
	if apiDeviceBlocked(t, api.URL, mac) {
		t.Fatal("a refused block reached the box")
	}

	// Giving access back is free forever: no token needed.
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/devices/" + mac + "/unblock", ""},
		{"POST", "/api/devices/" + mac + "/wifi-unblock", ""},
		{"POST", "/api/filter/toggle", `{"enabled":false}`},
		{"PATCH", "/api/wifi/ssids/1", `{"enabled":true}`},
	} {
		if code, body := send(t, tc.method, api.URL+tc.path, tc.body); code == http.StatusForbidden || body["code"] == "not_entitled" {
			t.Errorf("%s %s %s = %d %v; a give-back action was refused", tc.method, tc.path, tc.body, code, body)
		}
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	for _, want := range []entitlement.Feature{entitlement.FeatureUnblock, entitlement.FeatureRestoreWiFiAccess, entitlement.FeatureNetworkOn} {
		found := false
		for _, f := range gate.asked {
			found = found || f == want
		}
		if !found {
			t.Errorf("the gate was never asked about %s: %v", want, gate.asked)
		}
	}
}

// TestAPI_NoGateIsUngated: `kergui serve` and the CLI pass no gate, and
// every action works without a token (RFC-006).
func TestAPI_NoGateIsUngated(t *testing.T) {
	api := newAPI(t)
	if code, body := send(t, "POST", api.URL+"/api/devices/ac:bb:cc:00:00:11/block", ""); code != http.StatusOK {
		t.Fatalf("block without a gate = %d %v", code, body)
	}
}
