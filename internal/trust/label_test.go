package trust

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCleanLabel_TrimsControlsAndBoundsTheLength(t *testing.T) {
	cases := map[string]string{
		"  PC du salon  ":    "PC du salon",
		"Téléphone\nde\tAwa": "Téléphone de Awa",
		"a\x00b\x1b[31mc":    "ab[31mc",
		"evil‮gnp.exe":       "evilgnp.exe",
		"":                   "",
		"\x07\x08":           "",
		"Awa   et    Moussa": "Awa et Moussa",
	}
	for in, want := range cases {
		if got := CleanLabel(in); got != want {
			t.Errorf("CleanLabel(%q) = %q, want %q", in, got, want)
		}
	}
	long := CleanLabel(strings.Repeat("é", 200))
	if n := len([]rune(long)); n != MaxLabelLen {
		t.Fatalf("a long label kept %d runes, want %d", n, MaxLabelLen)
	}
}

func TestRenameRelay_ChangesOnlyTheLabel(t *testing.T) {
	owner, _ := Generate(rand.Reader)
	relay, _ := Generate(rand.Reader)
	b := Bundle{TenantID: "T1", Version: 1, Owner: owner.Public(), Relays: []Relay{
		{AgentID: "A1", Kind: "pc", Keys: relay.Public(), Label: "DESKTOP-42", MACs: []string{"aa:bb:cc:dd:ee:ff"}},
	}}
	if err := b.RenameRelay("A1", "  PC du\nsalon "); err != nil {
		t.Fatal(err)
	}
	r, _ := b.Relay("A1")
	if r.Label != "PC du salon" || r.Kind != "pc" || r.KeyID() != relay.KeyID() || len(r.MACs) != 1 {
		t.Fatalf("renamed relay = %+v", r)
	}
	if err := b.RenameRelay("A2", "x"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("renaming an unknown relay: %v", err)
	}
	if err := b.RenameRelay("A1", "\x00 "); !errors.Is(err, ErrMalformed) {
		t.Fatalf("an empty name: %v", err)
	}

	// The label never weighs in a trust decision: the relay's key is
	// trusted the same whatever it is called, and a relay named after the
	// owner is no owner.
	signed, err := Sign(owner, DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier("T1", owner.Public().Ed25519, time.Now())
	if err := v.AcceptBundle(signed); err != nil {
		t.Fatalf("a renamed relay's bundle is refused: %v", err)
	}
	if err := b.RenameRelay("A1", "Propriétaire"); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.RelayByKeyID(owner.KeyID()); ok {
		t.Fatal("a label made the owner a relay")
	}
}
