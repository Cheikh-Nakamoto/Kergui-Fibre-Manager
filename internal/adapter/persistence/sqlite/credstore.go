package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CredentialStore is raw encrypted-blob storage for router credentials. It holds
// only ciphertext — the crypto lives in the vault adapter, keeping this layer
// unaware of plaintext. It satisfies the vault's Blobs interface structurally.
type CredentialStore struct{ db *sql.DB }

// NewCredentialStore wires the store.
func NewCredentialStore(db *DB) *CredentialStore { return &CredentialStore{db: db.SQL()} }

// Put stores (or replaces) the encrypted credential blob for a router.
func (s *CredentialStore) Put(ctx context.Context, routerID string, salt, nonce, ciphertext []byte) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO credentials (router_id, salt, nonce, ciphertext, updated_at)
VALUES (?,?,?,?,?)
ON CONFLICT(router_id) DO UPDATE SET
  salt=excluded.salt, nonce=excluded.nonce, ciphertext=excluded.ciphertext, updated_at=excluded.updated_at`,
		routerID, salt, nonce, ciphertext, time.Now().Unix())
	return err
}

// Get returns the stored blob, ok=false when absent.
func (s *CredentialStore) Get(ctx context.Context, routerID string) (salt, nonce, ciphertext []byte, ok bool, err error) {
	row := s.db.QueryRowContext(ctx, `SELECT salt, nonce, ciphertext FROM credentials WHERE router_id=?`, routerID)
	err = row.Scan(&salt, &nonce, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, nil, false, err
	}
	return salt, nonce, ciphertext, true, nil
}

// Del removes the blob for a router.
func (s *CredentialStore) Del(ctx context.Context, routerID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE router_id=?`, routerID)
	return err
}
