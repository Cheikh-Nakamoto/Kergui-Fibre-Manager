package domain

import (
	"errors"
	"testing"
)

func TestBillingSettings_Amount(t *testing.T) {
	s := BillingSettings{PricePerGB: 500, MinAmount: 100}
	cases := []struct {
		bytes uint64
		want  int64
	}{
		{0, 0},
		{1, 100},                      // rounds up to 1 F, raised to the minimum
		{1_000_000_000, 500},          // exactly 1 GB
		{2_500_000_000, 1250},         // 2.5 GB
		{2_500_000_001, 1251},         // any fraction of a franc rounds up
		{150_000_000_000, 75000},      // 150 GB
		{18_000_000_000_000, 9000000}, // 18 TB: no overflow
	}
	for _, c := range cases {
		if got := s.Amount(c.bytes); got != c.want {
			t.Errorf("Amount(%d) = %d, want %d", c.bytes, got, c.want)
		}
	}
}

func TestNormalizeSenegalPhone(t *testing.T) {
	for _, in := range []string{"771234567", "77 123 45 67", "221771234567", "+221 77 123 45 67", "00221771234567"} {
		got, err := NormalizeSenegalPhone(in)
		if err != nil || got != "+221771234567" {
			t.Errorf("NormalizeSenegalPhone(%q) = %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "12345", "+33612345678", "791234567"} {
		if _, err := NormalizeSenegalPhone(in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("NormalizeSenegalPhone(%q) should fail, got %v", in, err)
		}
	}
}
