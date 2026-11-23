package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func decl(build int64) Declaration {
	return Declaration{
		Target:      Target{Platform: Android, Channel: Website},
		VersionName: "0.2.0", Build: build,
		URL:    "https://github.com/example/kergui/releases/download/v0.2.0/kergui.apk",
		SHA256: strings.Repeat("ab", 32), Size: 40_000_000,
	}
}

func TestDeclaration_Validate(t *testing.T) {
	if err := decl(21).Validate(); err != nil {
		t.Fatalf("a good declaration: %v", err)
	}
	for name, mut := range map[string]func(*Declaration){
		"platform":    func(d *Declaration) { d.Platform = "ios" },
		"channel":     func(d *Declaration) { d.Channel = "play" },
		"build zero":  func(d *Declaration) { d.Build = 0 },
		"build huge":  func(d *Declaration) { d.Build = MaxBuild + 1 },
		"no name":     func(d *Declaration) { d.VersionName = "" },
		"http":        func(d *Declaration) { d.URL = "http://example.org/a.apk" },
		"no url":      func(d *Declaration) { d.URL = "" },
		"short sha":   func(d *Declaration) { d.SHA256 = "abc" },
		"upper sha":   func(d *Declaration) { d.SHA256 = strings.Repeat("AB", 32) },
		"no size":     func(d *Declaration) { d.Size = 0 },
		"long notes":  func(d *Declaration) { d.Notes = strings.Repeat("é", MaxNotesLen+1) },
		"bad unicode": func(d *Declaration) { d.Notes = "\xff" },
	} {
		d := decl(21)
		mut(&d)
		if err := d.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	// Normalize lowercases the digest a tool may print in capitals.
	d := decl(21)
	d.SHA256 = " " + strings.Repeat("AB", 32) + " "
	if err := d.Normalize().Validate(); err != nil {
		t.Fatalf("normalized: %v", err)
	}
}

func TestValidateInstallationID(t *testing.T) {
	for _, ok := range []string{"abcdefgh", "3f9c1a2b-7d4e-4f00-9a1b-2c3d4e5f6a7b", strings.Repeat("a", 64)} {
		if err := ValidateInstallationID(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 65), "a b c d e f", "abcdefg/", "+221770000000"} {
		if err := ValidateInstallationID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRelease_Lifecycle(t *testing.T) {
	now := time.Now()
	r := Release{ID: "a", Declaration: decl(21), State: Pending}
	if _, err := r.Withdraw(now); !errors.Is(err, ErrState) {
		t.Fatalf("withdraw a pending build: %v", err)
	}
	r, err := r.Publish(now)
	if err != nil || r.State != Published || r.PublishedAt == nil {
		t.Fatalf("publish: %v %+v", err, r)
	}
	if _, err := r.Publish(now); !errors.Is(err, ErrState) {
		t.Fatalf("publish twice: %v", err)
	}
	if r, err = r.Withdraw(now); err != nil || r.State != Withdrawn || r.WithdrawnAt == nil {
		t.Fatalf("withdraw: %v %+v", err, r)
	}
	if r, err = r.Publish(now); err != nil || r.State != Published || r.WithdrawnAt != nil {
		t.Fatalf("publish again after a withdrawal: %v %+v", err, r)
	}
}

func TestRelease_Redeclare(t *testing.T) {
	r := Release{ID: "a", Declaration: decl(21), State: Pending}
	r.Notes = "Écrit dans le back-office."
	if _, changed, err := r.Redeclare(decl(21)); err != nil || changed {
		t.Fatalf("same file: changed=%v err=%v", changed, err)
	}
	rebuilt := decl(21)
	rebuilt.SHA256 = strings.Repeat("cd", 32)
	rebuilt.Notes = "Corrections."
	got, changed, err := r.Redeclare(rebuilt)
	if err != nil || !changed || got.SHA256 != rebuilt.SHA256 || got.Notes != "Écrit dans le back-office." || got.ID != "a" {
		t.Fatalf("a pending build takes the new file and keeps its notes: %v %v %+v", changed, err, got)
	}
	r.State = Published
	if _, _, err := r.Redeclare(rebuilt); !errors.Is(err, ErrFileChanged) {
		t.Fatalf("a published build changed: %v", err)
	}
	if _, changed, err := r.Redeclare(decl(21)); err != nil || changed {
		t.Fatalf("a published build declared again: %v %v", changed, err)
	}
}

func TestOfferMinimumAndCoverage(t *testing.T) {
	rs := []Release{
		{ID: "19", Declaration: decl(19), State: Published},
		{ID: "20", Declaration: decl(20), State: Published},
		{ID: "21", Declaration: decl(21), State: Withdrawn},
		{ID: "22", Declaration: decl(22), State: Pending},
	}
	if o := Offer(rs, 18); o == nil || o.Build != 20 {
		t.Fatalf("offer above 18 = %+v, want 20", o)
	}
	if o := Offer(rs, 20); o != nil {
		t.Fatalf("offer above 20 = %+v, want none", o)
	}
	if err := CheckMinimum(rs, 20); err != nil {
		t.Fatalf("minimum on a published build: %v", err)
	}
	for _, b := range []int64{21, 22, 23} {
		if err := CheckMinimum(rs, b); !errors.Is(err, ErrNotPublished) {
			t.Errorf("minimum %d: %v", b, err)
		}
	}
	if err := CheckMinimum(rs, 0); err != nil {
		t.Fatalf("clearing the minimum: %v", err)
	}
	if !Covered(rs, 19, "19") || Covered(rs, 20, "20") || !Covered(rs, 0, "20") {
		t.Fatal("coverage of the minimum")
	}
}
