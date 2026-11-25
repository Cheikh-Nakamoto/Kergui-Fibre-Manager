package handler

import "testing"

// TestMayCarry: POST router/commands carries the relay's allow-list for
// issue #50, and nothing else (the relay checks again).
func TestMayCarry(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/usage", true},
		{"GET", "/api/wifi/ssids", true},
		{"GET", "/api/wifi/blocked", true},
		{"PATCH", "/api/wifi/ssids/DEV.WIFI.AP1", true},
		{"POST", "/api/devices/ac:bb:cc:00:00:22/wifi-block", true},
		{"POST", "/api/devices/ac:bb:cc:00:00:22/wifi-unblock", true},
		{"POST", "/api/internet/cut", true},
		{"POST", "/api/internet/restore", true},
		{"POST", "/api/labels", true},
		{"GET", "/api/labels", false},
		{"POST", "/api/labels/x", false},

		{"POST", "/api/login", false},
		{"GET", "/api/discover", false},
		{"GET", "/api/usage?period=2026-01", false},
		{"GET", "/api/usage/devices/ac:bb:cc:00:00:22", false},
		{"PATCH", "/api/wifi/ssids/../../login", false},
		{"PATCH", "/api/wifi/ssids/", false},
		{"PATCH", "/api/wifi/ssids/a/b", false},
		{"POST", "/api/devices//wifi-block", false},
		{"GET", "/api/internet/cut", false},
	} {
		if got := mayCarry(tc.method, tc.path); got != tc.want {
			t.Errorf("mayCarry(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
