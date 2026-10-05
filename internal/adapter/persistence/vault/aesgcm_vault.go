// Package vault implements the credential vault: router credentials encrypted at
// rest with AES-256-GCM, under a key derived from a master passphrase via
// argon2id (brief §12). The plaintext password never touches disk, logs, or any
// API response. The ciphertext backend is abstracted behind Blobs, so the crypto
// is independent of the storage technology.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ErrNoMasterKey is returned when a vault operation needs the master passphrase
// but none was configured (set KERGUI_MASTER_KEY).
var ErrNoMasterKey = errors.New("master key not set (set KERGUI_MASTER_KEY)")

// Blobs is the ciphertext storage backend (implemented by the sqlite package).
type Blobs interface {
	Put(ctx context.Context, routerID string, salt, nonce, ciphertext []byte) error
	Get(ctx context.Context, routerID string) (salt, nonce, ciphertext []byte, ok bool, err error)
	Del(ctx context.Context, routerID string) error
}

// argon2id parameters — deliberately conservative for a local CLI.
const (
	saltLen    = 16
	keyLen     = 32 // AES-256
	argonTime  = 1
	argonMem   = 64 * 1024 // 64 MiB
	argonProcs = 4
)

// AESGCMVault encrypts credentials with a per-record salt and nonce.
type AESGCMVault struct {
	blobs      Blobs
	passphrase []byte
}

var _ port.CredentialVault = (*AESGCMVault)(nil)

// New builds a vault. An empty passphrase is permitted (commands that do not need
// credentials still work); Store/Load then fail with ErrNoMasterKey.
func New(blobs Blobs, passphrase string) *AESGCMVault {
	return &AESGCMVault{blobs: blobs, passphrase: []byte(passphrase)}
}

type credsDTO struct {
	Username string `json:"u"`
	Password string `json:"p"`
}

// Store encrypts and persists the credentials for a router.
func (v *AESGCMVault) Store(ctx context.Context, routerID string, c domain.Credentials) error {
	if len(v.passphrase) == 0 {
		return ErrNoMasterKey
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return err
	}
	gcm, err := v.gcm(salt)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	pt, err := json.Marshal(credsDTO{Username: c.Username, Password: c.Password})
	if err != nil {
		return err
	}
	ct := gcm.Seal(nil, nonce, pt, []byte(routerID)) // routerID as additional authenticated data
	return v.blobs.Put(ctx, routerID, salt, nonce, ct)
}

// Load decrypts the credentials for a router (ok=false when none are stored).
func (v *AESGCMVault) Load(ctx context.Context, routerID string) (domain.Credentials, bool, error) {
	if len(v.passphrase) == 0 {
		return domain.Credentials{}, false, ErrNoMasterKey
	}
	salt, nonce, ct, ok, err := v.blobs.Get(ctx, routerID)
	if err != nil || !ok {
		return domain.Credentials{}, ok, err
	}
	gcm, err := v.gcm(salt)
	if err != nil {
		return domain.Credentials{}, false, err
	}
	pt, err := gcm.Open(nil, nonce, ct, []byte(routerID))
	if err != nil {
		// Wrong passphrase or tampered data.
		return domain.Credentials{}, false, fmt.Errorf("decrypt credentials: %w", err)
	}
	var dto credsDTO
	if err := json.Unmarshal(pt, &dto); err != nil {
		return domain.Credentials{}, false, err
	}
	return domain.Credentials{Username: dto.Username, Password: dto.Password}, true, nil
}

// Delete removes the stored credentials for a router.
func (v *AESGCMVault) Delete(ctx context.Context, routerID string) error {
	return v.blobs.Del(ctx, routerID)
}

func (v *AESGCMVault) gcm(salt []byte) (cipher.AEAD, error) {
	key := argon2.IDKey(v.passphrase, salt, argonTime, argonMem, argonProcs, keyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
