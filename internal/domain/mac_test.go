package domain

import (
	"errors"
	"testing"
)

func TestParseMAC_Canonical(t *testing.T) {
	cases := map[string]string{
		"AA:BB:CC:DD:EE:FF":   "aa:bb:cc:dd:ee:ff",
		"aa-bb-cc-dd-ee-ff":   "aa:bb:cc:dd:ee:ff",
		"aabb.ccdd.eeff":      "aa:bb:cc:dd:ee:ff",
		"AABBCCDDEEFF":        "aa:bb:cc:dd:ee:ff",
		" a0:b1:c2:d3:e4:f5 ": "a0:b1:c2:d3:e4:f5",
	}
	for in, want := range cases {
		got, err := ParseMAC(in)
		if err != nil {
			t.Fatalf("ParseMAC(%q) unexpected error: %v", in, err)
		}
		if got.String() != want {
			t.Errorf("ParseMAC(%q) = %q, want %q", in, got.String(), want)
		}
	}
}

func TestParseMAC_Invalid(t *testing.T) {
	for _, in := range []string{"", "aa:bb:cc:dd:ee", "ZZ:bb:cc:dd:ee:ff", "aabbccddeeff00", "hello"} {
		if _, err := ParseMAC(in); !errors.Is(err, ErrInvalidMAC) {
			t.Errorf("ParseMAC(%q) error = %v, want ErrInvalidMAC", in, err)
		}
	}
}

func TestMAC_ZeroEqualOUI(t *testing.T) {
	var zero MAC
	if !zero.IsZero() {
		t.Error("zero MAC should report IsZero")
	}
	if zero.OUI() != "" {
		t.Errorf("zero OUI = %q, want empty", zero.OUI())
	}
	m := MustMAC("aa:bb:cc:dd:ee:ff")
	if m.IsZero() {
		t.Error("non-zero MAC reported IsZero")
	}
	if m.OUI() != "aa:bb:cc" {
		t.Errorf("OUI = %q, want aa:bb:cc", m.OUI())
	}
	if !m.Equal(MustMAC("AA-BB-CC-DD-EE-FF")) {
		t.Error("Equal should ignore notation/case")
	}
}
