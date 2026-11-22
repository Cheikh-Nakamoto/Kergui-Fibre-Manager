package ztef6600p

import (
	"strings"
	"testing"
)

// TestMeta_ListsWiFiAccessControl: the Wi-Fi access control the gateway
// implements (wifi.go) is documented in the adapter metadata, like block and
// unblock, so `kergui inspect` and docs show what "Couper l'accès" calls.
func TestMeta_ListsWiFiAccessControl(t *testing.T) {
	want := map[string]struct{ method, tag string }{
		"wifi_blocked": {"GET", wlanRuleTag},
		"wifi_block":   {"POST", wlanRuleTag},
		"wifi_unblock": {"POST", wlanRuleTag},
	}
	for _, e := range Meta().Endpoints {
		w, ok := want[e.Operation]
		if !ok {
			continue
		}
		if e.Method != w.method || !strings.Contains(e.Path, w.tag) {
			t.Errorf("%s = %s %s, want %s on %s", e.Operation, e.Method, e.Path, w.method, w.tag)
		}
		if e.Notes == "" {
			t.Errorf("%s has no notes", e.Operation)
		}
		delete(want, e.Operation)
	}
	for op := range want {
		t.Errorf("operation %s missing from Meta().Endpoints", op)
	}
}
