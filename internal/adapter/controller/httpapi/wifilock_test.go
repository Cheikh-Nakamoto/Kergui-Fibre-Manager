package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

// A server without the closed Wi-Fi (`kergui serve`, for now the relay: #109)
// answers its routes 501, and a block with « Le déconnecter aussi du Wi-Fi »
// is the plain block plus the plain Wi-Fi refusal.
func TestAPI_WiFiLockWithoutTheUseCase(t *testing.T) {
	box := mockrouter.NewF6600P("admin", "admin")
	api := newAPIFor(t, box.Handler(), domain.Credentials{Username: "admin", Password: "admin"})
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/wifi/lock", ""},
		{"POST", "/api/wifi/lock/close", `{"preview":true}`},
		{"POST", "/api/wifi/lock/open", ""},
		{"POST", "/api/wifi/lock/window", `{"action":"open"}`},
	} {
		if code, body := send(t, tc.method, api.URL+tc.path, tc.body); code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d %v, want 501", tc.method, tc.path, code, body)
		}
	}

	const mac = "ac:bb:cc:00:00:11"
	if code, body := send(t, "POST", api.URL+"/api/devices/"+mac+"/block", `{"cut_wifi":true}`); code != http.StatusOK || body["cut_wifi"] != true {
		t.Fatalf("block and cut = %d %v", code, body)
	}
	if !box.Blocked(mac) || !box.WiFiBlocked(mac) {
		t.Fatalf("blocked %v, refused at the Wi-Fi %v; want both", box.Blocked(mac), box.WiFiBlocked(mac))
	}
	if code, body := send(t, "POST", api.URL+"/api/devices/"+mac+"/unblock", `{"cut_wifi":true}`); code != http.StatusOK || body["cut_wifi"] != false {
		t.Fatalf("unblock = %d %v", code, body)
	}
}
