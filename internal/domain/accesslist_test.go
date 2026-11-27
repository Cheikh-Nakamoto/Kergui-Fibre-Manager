package domain_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// TestAccessListFullError (RFC-007 D6): the error carries the list, its limit
// and its count, and matches ErrAccessListFull through any wrapping.
func TestAccessListFullError(t *testing.T) {
	var err error = &domain.AccessListFullError{Kind: domain.AccessListFirewall, Limit: 32, Used: 32}
	wrapped := fmt.Errorf("block: %w", err)
	if !errors.Is(wrapped, domain.ErrAccessListFull) {
		t.Fatalf("%v does not match ErrAccessListFull", wrapped)
	}
	var full *domain.AccessListFullError
	if !errors.As(wrapped, &full) || full.Kind != "firewall" || full.Limit != 32 || full.Used != 32 {
		t.Fatalf("errors.As = %+v", full)
	}
	if got := err.Error(); got != "router: access list full (firewall, 32/32)" {
		t.Errorf("Error() = %q", got)
	}
	unknown := &domain.AccessListFullError{Kind: domain.AccessListWiFi, Used: 21}
	if got := unknown.Error(); got != "router: access list full (wifi, 21 used, limit unknown)" {
		t.Errorf("Error() = %q", got)
	}
}

// TestAbsentFor30Days: 30 days to the second is absent; one second less is
// not; a device the inventory never dated is never absent.
func TestAbsentFor30Days(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		lastSeen time.Time
		want     bool
	}{
		{"30 days exactly", now.Add(-30 * 24 * time.Hour), true},
		{"one second short of 30 days", now.Add(-30*24*time.Hour + time.Second), false},
		{"seen today", now.Add(-time.Hour), false},
		{"a year ago", now.AddDate(-1, 0, 0), true},
		{"never dated", time.Time{}, false},
	}
	for _, c := range cases {
		if got := domain.AbsentLongEnough(c.lastSeen, now); got != c.want {
			t.Errorf("%s: AbsentLongEnough = %v, want %v", c.name, got, c.want)
		}
	}
}
