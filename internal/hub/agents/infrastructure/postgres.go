// Package infrastructure implements the agents module's repositories on
// Postgres. Queries go through txctx.DB so they join the caller's
// transaction when there is one, as the kernel's own repositories do.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
)

// Repo implements every agents repository on one pool. They share a type
// because they share a transaction more often than not: redeeming a code and
// creating the agent is one write.
type Repo struct{ pool *pgxpool.Pool }

var (
	_ domain.Repository        = (*Repo)(nil)
	_ domain.PairingRepository = (*Repo)(nil)
	_ domain.TrustRepository   = (*Repo)(nil)
)

// New wires the repositories.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// --- agents ---------------------------------------------------------------

const agentColumns = `id, tenant_id, name, kind, COALESCE(user_id::text, ''), ed25519, x25519, key_id, created_at, revoked_at, last_seen_at`

// Create stores a newly paired agent with the hash of its token.
func (r *Repo) Create(ctx context.Context, a *domain.Agent, tokenHash []byte) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_agents (id, tenant_id, name, kind, user_id, token_hash, ed25519, x25519, key_id, created_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6, $7, $8, $9, $10)`,
		a.ID, a.TenantID, a.Name, string(a.Kind), a.UserID, tokenHash, a.Ed25519, a.X25519, a.KeyID, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("agents: create: %w", err)
	}
	return nil
}

// ByID returns one agent of a household.
func (r *Repo) ByID(ctx context.Context, tenantID, id string) (*domain.Agent, error) {
	return r.one(ctx, `SELECT `+agentColumns+` FROM hub_agents WHERE tenant_id = $1 AND id = $2`, tenantID, id)
}

// ByTokenHash resolves a relay's bearer token, revoked agents included: the
// caller answers 401 rather than leaking whether the token ever existed.
func (r *Repo) ByTokenHash(ctx context.Context, tokenHash []byte) (*domain.Agent, error) {
	return r.one(ctx, `SELECT `+agentColumns+` FROM hub_agents WHERE token_hash = $1`, tokenHash)
}

func (r *Repo) one(ctx context.Context, query string, args ...any) (*domain.Agent, error) {
	var (
		a    domain.Agent
		kind string
	)
	err := r.db(ctx).QueryRow(ctx, query, args...).Scan(
		&a.ID, &a.TenantID, &a.Name, &kind, &a.UserID, &a.Ed25519, &a.X25519, &a.KeyID,
		&a.CreatedAt, &a.RevokedAt, &a.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agents: read: %w", err)
	}
	a.Kind = domain.Kind(kind)
	return &a, nil
}

// ListByTenant returns the household's agents, newest first.
func (r *Repo) ListByTenant(ctx context.Context, tenantID string) ([]*domain.Agent, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT `+agentColumns+` FROM hub_agents WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("agents: list: %w", err)
	}
	defer rows.Close()
	var out []*domain.Agent
	for rows.Next() {
		var (
			a    domain.Agent
			kind string
		)
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &kind, &a.UserID, &a.Ed25519, &a.X25519, &a.KeyID,
			&a.CreatedAt, &a.RevokedAt, &a.LastSeenAt); err != nil {
			return nil, fmt.Errorf("agents: list scan: %w", err)
		}
		a.Kind = domain.Kind(kind)
		out = append(out, &a)
	}
	return out, rows.Err()
}

// Revoke marks an agent as revoked, once.
func (r *Repo) Revoke(ctx context.Context, tenantID, id string, at time.Time) error {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_agents SET revoked_at = COALESCE(revoked_at, $3) WHERE tenant_id = $1 AND id = $2`,
		tenantID, id, at)
	if err != nil {
		return fmt.Errorf("agents: revoke: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// TouchSeen records a heartbeat. It is best-effort bookkeeping, never a
// decision input.
func (r *Repo) TouchSeen(ctx context.Context, id string, at time.Time) error {
	_, err := r.db(ctx).Exec(ctx, `UPDATE hub_agents SET last_seen_at = $2 WHERE id = $1`, id, at)
	return err
}

// --- pairing codes --------------------------------------------------------

// CreatePairing stores a pairing code's hash.
func (r *Repo) CreatePairing(ctx context.Context, codeHash []byte, p domain.Pairing) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_pairing_codes (code_hash, tenant_id, created_by, kind, for_user_id, expires_at)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6)`,
		codeHash, p.TenantID, p.CreatedBy, string(p.Kind), p.ForUserID, p.ExpiresAt)
	if err != nil {
		return fmt.Errorf("agents: create pairing: %w", err)
	}
	return nil
}

// Redeem consumes a code. The UPDATE itself enforces single use: two relays
// racing on the same code produce one winner and one ErrPairingNotFound,
// without a transaction or a lock.
func (r *Repo) Redeem(ctx context.Context, codeHash []byte, now time.Time) (domain.Pairing, error) {
	var (
		p    domain.Pairing
		kind string
	)
	err := r.db(ctx).QueryRow(ctx,
		`UPDATE hub_pairing_codes SET used_at = $2
		 WHERE code_hash = $1 AND used_at IS NULL AND expires_at > $2
		 RETURNING tenant_id, created_by, kind, COALESCE(for_user_id::text, ''), expires_at, used_at`,
		codeHash, now).Scan(&p.TenantID, &p.CreatedBy, &kind, &p.ForUserID, &p.ExpiresAt, &p.UsedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Pairing{}, domain.ErrPairingNotFound
	}
	if err != nil {
		return domain.Pairing{}, fmt.Errorf("agents: redeem: %w", err)
	}
	p.Kind = domain.Kind(kind)
	return p, nil
}

// PurgePairings drops codes that are spent or long expired.
func (r *Repo) PurgePairings(ctx context.Context, before time.Time) error {
	_, err := r.db(ctx).Exec(ctx, `DELETE FROM hub_pairing_codes WHERE expires_at < $1`, before)
	return err
}

// --- trust material -------------------------------------------------------

// GetBundle returns the household's signed bundle.
func (r *Repo) GetBundle(ctx context.Context, tenantID string) (domain.TrustBundle, error) {
	var b domain.TrustBundle
	err := r.db(ctx).QueryRow(ctx,
		`SELECT tenant_id, version, payload, sig, updated_at FROM hub_trust_bundles WHERE tenant_id = $1`,
		tenantID).Scan(&b.TenantID, &b.Version, &b.Payload, &b.Sig, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.TrustBundle{}, domain.ErrNoBundle
	}
	if err != nil {
		return domain.TrustBundle{}, fmt.Errorf("agents: read bundle: %w", err)
	}
	return b, nil
}

// PutBundle stores a bundle, refusing anything that is not strictly newer.
// The WHERE clause is what makes a rollback impossible even under a race.
func (r *Repo) PutBundle(ctx context.Context, b domain.TrustBundle) error {
	tag, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_trust_bundles (tenant_id, version, payload, sig, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (tenant_id) DO UPDATE
		 SET version = EXCLUDED.version, payload = EXCLUDED.payload, sig = EXCLUDED.sig, updated_at = now()
		 WHERE hub_trust_bundles.version < EXCLUDED.version`,
		b.TenantID, int64(b.Version), b.Payload, b.Sig)
	if err != nil {
		return fmt.Errorf("agents: write bundle: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrStaleVersion
	}
	return nil
}

// GetCredentials returns the sealed box password for one relay.
func (r *Repo) GetCredentials(ctx context.Context, tenantID, agentID string) (domain.SealedCredentials, error) {
	var c domain.SealedCredentials
	err := r.db(ctx).QueryRow(ctx,
		`SELECT tenant_id, agent_id, blob, version, updated_at FROM hub_sealed_credentials
		 WHERE tenant_id = $1 AND agent_id = $2`,
		tenantID, agentID).Scan(&c.TenantID, &c.AgentID, &c.Blob, &c.Version, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SealedCredentials{}, domain.ErrNoCredentials
	}
	if err != nil {
		return domain.SealedCredentials{}, fmt.Errorf("agents: read credentials: %w", err)
	}
	return c, nil
}

// PutCredentials stores a sealed blob, newest version wins.
func (r *Repo) PutCredentials(ctx context.Context, c domain.SealedCredentials) error {
	tag, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_sealed_credentials (agent_id, tenant_id, blob, version, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (agent_id) DO UPDATE
		 SET blob = EXCLUDED.blob, version = EXCLUDED.version, updated_at = now()
		 WHERE hub_sealed_credentials.version < EXCLUDED.version`,
		c.AgentID, c.TenantID, c.Blob, int64(c.Version))
	if err != nil {
		return fmt.Errorf("agents: write credentials: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrStaleVersion
	}
	return nil
}

// GetNetwork returns the Wi-Fi signature and router pin. A household that
// never registered one gets an empty Network, not an error: the relay then
// stays in standby with a reason, which is the right answer.
func (r *Repo) GetNetwork(ctx context.Context, tenantID string) (domain.Network, error) {
	n := domain.Network{TenantID: tenantID}
	err := r.db(ctx).QueryRow(ctx,
		`SELECT ssids, bssids, router_pin, updated_at FROM hub_networks WHERE tenant_id = $1`,
		tenantID).Scan(&n.SSIDs, &n.BSSIDs, &n.RouterPin, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return n, nil
	}
	if err != nil {
		return domain.Network{}, fmt.Errorf("agents: read network: %w", err)
	}
	return n, nil
}

// PutNetwork replaces the household's network signature.
func (r *Repo) PutNetwork(ctx context.Context, n domain.Network) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_networks (tenant_id, ssids, bssids, router_pin, updated_at)
		 VALUES ($1, $2, $3, $4, now())
		 ON CONFLICT (tenant_id) DO UPDATE
		 SET ssids = EXCLUDED.ssids, bssids = EXCLUDED.bssids, router_pin = EXCLUDED.router_pin, updated_at = now()`,
		n.TenantID, n.SSIDs, n.BSSIDs, n.RouterPin)
	if err != nil {
		return fmt.Errorf("agents: write network: %w", err)
	}
	return nil
}
