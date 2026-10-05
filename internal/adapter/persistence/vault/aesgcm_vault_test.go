package vault_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

type memBlobs struct {
	salt, nonce, ct map[string][]byte
}

func newMem() *memBlobs {
	return &memBlobs{salt: map[string][]byte{}, nonce: map[string][]byte{}, ct: map[string][]byte{}}
}
func (m *memBlobs) Put(_ context.Context, id string, salt, nonce, ct []byte) error {
	m.salt[id], m.nonce[id], m.ct[id] = salt, nonce, ct
	return nil
}
func (m *memBlobs) Get(_ context.Context, id string) ([]byte, []byte, []byte, bool, error) {
	ct, ok := m.ct[id]
	if !ok {
		return nil, nil, nil, false, nil
	}
	return m.salt[id], m.nonce[id], ct, true, nil
}
func (m *memBlobs) Del(_ context.Context, id string) error {
	delete(m.salt, id)
	delete(m.nonce, id)
	delete(m.ct, id)
	return nil
}

func TestVault_Roundtrip(t *testing.T) {
	ctx := context.Background()
	blobs := newMem()
	v := vault.New(blobs, "correct horse battery staple")
	creds := domain.Credentials{Username: "admin", Password: "s3cr3t"}

	if err := v.Store(ctx, "r", creds); err != nil {
		t.Fatalf("store: %v", err)
	}
	// Ciphertext must not contain the plaintext password.
	if string(blobs.ct["r"]) == "" || contains(blobs.ct["r"], "s3cr3t") {
		t.Fatal("password appears in ciphertext (or empty)")
	}
	got, ok, err := v.Load(ctx, "r")
	if err != nil || !ok || got.Username != "admin" || got.Password != "s3cr3t" {
		t.Fatalf("load = (%v, %v, %v)", got, ok, err)
	}
}

func TestVault_WrongPassphrase(t *testing.T) {
	ctx := context.Background()
	blobs := newMem()
	_ = vault.New(blobs, "right-pass").Store(ctx, "r", domain.Credentials{Username: "a", Password: "b"})

	_, _, err := vault.New(blobs, "wrong-pass").Load(ctx, "r")
	if err == nil {
		t.Fatal("expected decryption error with wrong passphrase")
	}
}

func TestVault_NoMasterKey(t *testing.T) {
	ctx := context.Background()
	v := vault.New(newMem(), "")
	if err := v.Store(ctx, "r", domain.Credentials{Username: "a", Password: "b"}); !errors.Is(err, vault.ErrNoMasterKey) {
		t.Fatalf("store err = %v, want ErrNoMasterKey", err)
	}
	if _, _, err := v.Load(ctx, "r"); !errors.Is(err, vault.ErrNoMasterKey) {
		t.Fatalf("load err = %v, want ErrNoMasterKey", err)
	}
}

func TestVault_LoadMissing(t *testing.T) {
	_, ok, err := vault.New(newMem(), "pass").Load(context.Background(), "nope")
	if err != nil || ok {
		t.Fatalf("missing load = (ok=%v, err=%v)", ok, err)
	}
}

func contains(b []byte, sub string) bool {
	s, t := string(b), sub
	for i := 0; i+len(t) <= len(s); i++ {
		if s[i:i+len(t)] == t {
			return true
		}
	}
	return false
}
