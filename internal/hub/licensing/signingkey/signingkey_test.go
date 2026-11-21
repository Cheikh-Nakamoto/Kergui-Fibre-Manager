package signingkey

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParse_AcceptsTheUsualEncodings(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	want := ed25519.NewKeyFromSeed(seed)
	der, err := x509.MarshalPKCS8PrivateKey(want)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	for name, raw := range map[string]string{
		"pem (openssl genpkey -algorithm ed25519)": string(pemKey),
		"base64 seed":         base64.StdEncoding.EncodeToString(seed),
		"base64url seed":      base64.RawURLEncoding.EncodeToString(seed),
		"base64 private key":  base64.StdEncoding.EncodeToString(want),
		"hex seed":            hex.EncodeToString(seed),
		"base64 with newline": base64.StdEncoding.EncodeToString(seed) + "\n",
	} {
		got, err := Parse([]byte(raw))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("%s: another key", name)
		}
	}
}

func TestParse_Refuses(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":       "",
		"short":       base64.StdEncoding.EncodeToString([]byte("short")),
		"garbage":     "not a key at all",
		"rsa-ish pem": "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n",
	} {
		if _, err := Parse([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// A seed whose public half does not match the 64-byte key is refused.
	k := ed25519.NewKeyFromSeed(make([]byte, 32))
	bad := append([]byte(nil), k...)
	bad[40] ^= 1
	if _, err := Parse([]byte(base64.StdEncoding.EncodeToString(bad))); !errors.Is(err, ErrInvalid) {
		t.Errorf("mismatched halves: err = %v", err)
	}
}

func TestLoad_FromValueOrFile(t *testing.T) {
	seed := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if k, err := Load(seed, ""); err != nil || k == nil {
		t.Fatalf("value: %v", err)
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, err := Load("", path); err != nil || k == nil {
		t.Fatalf("file: %v", err)
	}
	if k, err := Load("", ""); err != nil || k != nil {
		t.Fatalf("none: %v %v", k, err)
	}
	if _, err := Load(seed, path); err == nil {
		t.Fatal("both set must be refused")
	}
	if _, err := Load("", filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file must be refused")
	}
}

func TestKeyID_IsStableAndShort(t *testing.T) {
	pub := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	if a, b := KeyID(pub), KeyID(pub); a != b || len(a) != 16 {
		t.Fatalf("%q %q", a, b)
	}
}
