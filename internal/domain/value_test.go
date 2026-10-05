package domain

import (
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
