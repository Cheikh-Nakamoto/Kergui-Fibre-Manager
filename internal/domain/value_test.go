package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseIP(t *testing.T) {
	if ip, err := ParseIP(""); err != nil || !ip.IsZero() {
		t.Errorf("empty IP: got (%q, %v), want zero IP nil error", ip, err)
	}
	if ip, err := ParseIP("192.168.1.10"); err != nil || ip.String() != "192.168.1.10" {
		t.Errorf("v4 IP: got (%q, %v)", ip, err)
	}
	if _, err := ParseIP("999.1.1.1"); !errors.Is(err, ErrInvalidIP) {
		t.Errorf("invalid IP error = %v, want ErrInvalidIP", err)
	}
}

func TestParseAccessMode(t *testing.T) {
	block := []string{"block", "Bloc", "bloqué", "BLACK", "blacklist", "deny", "ban"}
	permit := []string{"permit", "Autorisé", "autorise", "white", "whitelist", "allow"}
	for _, s := range block {
		if m, err := ParseAccessMode(s); err != nil || m != AccessBlock {
			t.Errorf("ParseAccessMode(%q) = (%v, %v), want block", s, m, err)
		}
	}
	for _, s := range permit {
		if m, err := ParseAccessMode(s); err != nil || m != AccessPermit {
			t.Errorf("ParseAccessMode(%q) = (%v, %v), want permit", s, m, err)
		}
	}
	if _, err := ParseAccessMode("maybe"); !errors.Is(err, ErrInvalidAccessMode) {
		t.Errorf("unknown mode error = %v, want ErrInvalidAccessMode", err)
	}
	if AccessBlock.Valid() != true || AccessMode("x").Valid() != false {
		t.Error("Valid() wrong")
	}
}

func TestCredentialsRedaction(t *testing.T) {
	c := Credentials{Username: "admin", Password: "s3cr3t"}
	if strings.Contains(c.String(), "s3cr3t") || strings.Contains(c.GoString(), "s3cr3t") {
		t.Fatalf("password leaked in string form: %s / %s", c.String(), c.GoString())
	}
	if !c.HasPassword() {
		t.Error("HasPassword should be true")
	}
}

func TestDeviceDisplayName(t *testing.T) {
	m := MustMAC("aa:bb:cc:dd:ee:ff")
	if got := (Device{MAC: m}).DisplayName(); got != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("DisplayName fallback to MAC = %q", got)
	}
	if got := (Device{MAC: m, Hostname: "galaxy"}).DisplayName(); got != "galaxy" {
		t.Errorf("DisplayName hostname = %q", got)
	}
	if got := (Device{MAC: m, Hostname: "galaxy", CustomName: "Téléphone Maman"}).DisplayName(); got != "Téléphone Maman" {
		t.Errorf("DisplayName custom = %q", got)
	}
}

// TestMACAndIPRoundTripJSON: both value objects write themselves as strings,
// so both must read themselves back. The relay depends on it to find its own
// address in a device list and refuse to cut itself.
func TestMACAndIPRoundTripJSON(t *testing.T) {
	type station struct {
		MAC MAC `json:"MAC"`
		IP  IP  `json:"IP"`
	}
	in := station{MAC: MustMAC("AC-BB-CC-00-00-11"), IP: MustIP("192.168.1.10")}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out station
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("a structure cannot read back its own JSON: %v", err)
	}
	if !out.MAC.Equal(in.MAC) || out.IP.String() != in.IP.String() {
		t.Errorf("round trip gave %+v, want %+v", out, in)
	}

	// The zero values travel as empty strings and come back unknown.
	raw, err = json.Marshal(station{})
	if err != nil {
		t.Fatal(err)
	}
	out = station{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("the unknown MAC and IP are not an error: %v", err)
	}
	if !out.MAC.IsZero() || !out.IP.IsZero() {
		t.Errorf("round trip of the zero values gave %+v", out)
	}

	for _, bad := range []string{`{"MAC":"not-a-mac"}`, `{"IP":"299.1.1.1"}`} {
		if err := json.Unmarshal([]byte(bad), &station{}); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
}
