package agent

import (
	"errors"
	"net/http"
	"testing"
)

// TestCheckAllowed is the relay's last line between the hub and the box: a
// signed command still only reaches what this list names (RFC-005, rule 4).
func TestCheckAllowed(t *testing.T) {
	tests := []struct {
		method, path string
		wantTarget   string
		wantErr      bool
	}{
		// What a household actually does.
		{http.MethodGet, "/api/devices", "", false},
		{http.MethodGet, "/api/wifi/ssids", "", false},
		{http.MethodGet, "/api/wifi/blocked", "", false},
		{http.MethodGet, "/api/filter", "", false},
		{http.MethodGet, "/api/usage", "", false},
		{http.MethodPost, "/api/filter/toggle", "", false},
		{http.MethodPost, "/api/devices/ac:bb:cc:00:00:22/block", "ac:bb:cc:00:00:22", false},
		{http.MethodPost, "/api/devices/ac:bb:cc:00:00:22/unblock", "ac:bb:cc:00:00:22", false},
		{http.MethodPost, "/api/devices/ac:bb:cc:00:00:22/wifi-block", "ac:bb:cc:00:00:22", false},
		{http.MethodPost, "/api/devices/ac:bb:cc:00:00:22/wifi-unblock", "ac:bb:cc:00:00:22", false},
		// Issue #50: SSID on / off and rename, and the whole house.
		{http.MethodPatch, "/api/wifi/ssids/DEV.WIFI.AP1", "", false},
		{http.MethodPost, "/api/internet/cut", "", false},
		{http.MethodPost, "/api/internet/restore", "", false},

		// The two routes RFC-005 names explicitly: logging in would let a
		// command repoint the relay, discovery would make it an SSRF probe.
		{http.MethodPost, "/api/login", "", true},
		{http.MethodGet, "/api/discover", "", true},
		// ... including by walking back up a path.
		{http.MethodPost, "/api/devices/../login", "", true},
		{http.MethodGet, "/api/devices/../../api/discover", "", true},
		{http.MethodGet, "/api/devices?x=/api/discover", "", true},

		// The rest of the API is simply not reachable from the hub.
		{http.MethodGet, "/api/config/env", "", true},
		// Setting a new box's first password (#92) is the owner's, at home.
		{http.MethodPost, "/api/box/first-password", "", true},
		{http.MethodGet, "/api/logs", "", true},
		{http.MethodGet, "/api/members", "", true},
		{http.MethodPost, "/api/payments/1/mark-paid", "", true},
		{http.MethodGet, "/api/usage/devices/ac:bb:cc:00:00:22", "", true},
		{http.MethodPost, "/api/usage/sample", "", true},
		{http.MethodPost, "/api/autoreject/run", "", true},
		{http.MethodPut, "/api/wifi/ssids/DEV.WIFI.AP1", "", true},
		{http.MethodPatch, "/api/wifi/ssids", "", true},
		{http.MethodGet, "/api/internet/cut", "", true},
		{http.MethodDelete, "/api/devices/ac:bb:cc:00:00:22/block", "", true},

		// Shape mistakes.
		{http.MethodPost, "/api/devices//block", "", true},
		{http.MethodPost, "/api/devices/a/b/block", "", true},
		{http.MethodGet, "/api/devices/ac:bb:cc:00:00:22", "", true},
	}
	for _, tc := range tests {
		target, err := checkAllowed(tc.method, tc.path)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s %s was allowed", tc.method, tc.path)
			} else if !errors.Is(err, ErrNotAllowed) {
				t.Errorf("%s %s: err = %v, want ErrNotAllowed", tc.method, tc.path, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s %s was refused: %v", tc.method, tc.path, err)
		}
		if target != tc.wantTarget {
			t.Errorf("%s %s target = %q, want %q", tc.method, tc.path, target, tc.wantTarget)
		}
	}
}

// TestCheckAllowed_TrailingSlash: a trailing slash must not be a second,
// unlisted spelling of a route.
func TestCheckAllowed_TrailingSlash(t *testing.T) {
	if _, err := checkAllowed(http.MethodGet, "/api/devices/"); err != nil {
		t.Errorf("a trailing slash was treated as a different route: %v", err)
	}
}

func TestSameMAC(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"ac:bb:cc:00:00:22", "AC-BB-CC-00-00-22", true},
		{"ac:bb:cc:00:00:22", " acbbcc000022 ", true},
		{"ac:bb:cc:00:00:22", "ac:bb:cc:00:00:23", false},
		{"", "", true},
	} {
		if got := sameMAC(tc.a, tc.b); got != tc.want {
			t.Errorf("sameMAC(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestAllowList_IsExactlyIssue50: the list covers exactly these routes, no
// more; adding one is a decision this test makes visible.
func TestAllowList_IsExactlyIssue50(t *testing.T) {
	want := []string{
		"GET /api/devices", "GET /api/wifi/ssids", "GET /api/wifi/blocked", "GET /api/filter", "GET /api/usage",
		"POST /api/filter/toggle",
		"POST /api/devices/+/block", "POST /api/devices/+/unblock",
		"POST /api/devices/+/wifi-block", "POST /api/devices/+/wifi-unblock",
		"PATCH /api/wifi/ssids/+",
		"POST /api/internet/cut", "POST /api/internet/restore",
	}
	if len(allowed) != len(want) {
		t.Fatalf("%d routes allowed, want %d", len(allowed), len(want))
	}
	for i, r := range allowed {
		if got := r.method + " " + r.pattern; got != want[i] {
			t.Errorf("route %d = %s, want %s", i, got, want[i])
		}
	}
}
