// Package infrastructure keeps the backups on Postgres. Queries go through
// txctx.DB so they join the transaction InBox opens.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/usecase"
)

// Repo is the backups store.
type Repo struct{ pool *pgxpool.Pool }

var _ usecase.Store = (*Repo)(nil)

// New wires the store.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// InBox takes a transaction-scoped advisory lock on the household's box, so
// two sources sending at once merge one after the other.
func (r *Repo) InBox(ctx context.Context, tenantID, boxID string, fn func(context.Context) error) error {
	return txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		if _, err := r.db(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('hub_backups/' || $1 || '/' || $2))`,
			tenantID, boxID); err != nil {
			return fmt.Errorf("backups: lock: %w", err)
		}
		return fn(ctx)
	})
}

// Current is the box's backup.
func (r *Repo) Current(ctx context.Context, tenantID, boxID string) (domain.Backup, error) {
	b := domain.Backup{TenantID: tenantID, BoxID: boxID}
	var kind string
	err := r.db(ctx).QueryRow(ctx, `SELECT archive, source_kind, source_id, devices, months, updated_at
		FROM hub_backups WHERE tenant_id = $1 AND box_id = $2`, tenantID, boxID).
		Scan(&b.Archive, &kind, &b.Source.ID, &b.Devices, &b.Months, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Backup{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Backup{}, fmt.Errorf("backups: current: %w", err)
	}
	b.Source.Kind = domain.SourceKind(kind)
	b.UpdatedAt = b.UpdatedAt.UTC()
	return b, nil
}

// List is the household's backups without their archives, by box.
func (r *Repo) List(ctx context.Context, tenantID string) ([]domain.Backup, error) {
	rows, err := r.db(ctx).Query(ctx, `SELECT box_id, source_kind, source_id, devices, months, updated_at
		FROM hub_backups WHERE tenant_id = $1 ORDER BY box_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("backups: list: %w", err)
	}
	defer rows.Close()
	out := []domain.Backup{}
	for rows.Next() {
		b := domain.Backup{TenantID: tenantID}
		var kind string
		if err := rows.Scan(&b.BoxID, &kind, &b.Source.ID, &b.Devices, &b.Months, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("backups: list: %w", err)
		}
		b.Source.Kind = domain.SourceKind(kind)
		b.UpdatedAt = b.UpdatedAt.UTC()
		out = append(out, b)
	}
	return out, rows.Err()
}

// Put stores the box's current backup.
func (r *Repo) Put(ctx context.Context, b domain.Backup) error {
	if _, err := r.db(ctx).Exec(ctx, `
INSERT INTO hub_backups (tenant_id, box_id, archive, source_kind, source_id, devices, months, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, box_id) DO UPDATE SET
  archive = excluded.archive, source_kind = excluded.source_kind, source_id = excluded.source_id,
  devices = excluded.devices, months = excluded.months, updated_at = excluded.updated_at`,
		b.TenantID, b.BoxID, b.Archive, string(b.Source.Kind), b.Source.ID, b.Devices, b.Months, b.UpdatedAt); err != nil {
		return fmt.Errorf("backups: put: %w", err)
	}
	return nil
}

// KeepVersion files a replaced backup.
func (r *Repo) KeepVersion(ctx context.Context, b domain.Backup, replacedAt time.Time) error {
	if _, err := r.db(ctx).Exec(ctx, `
INSERT INTO hub_backup_versions (tenant_id, box_id, archive, source_kind, source_id, devices, months, updated_at, replaced_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		b.TenantID, b.BoxID, b.Archive, string(b.Source.Kind), b.Source.ID, b.Devices, b.Months, b.UpdatedAt, replacedAt); err != nil {
		return fmt.Errorf("backups: keep version: %w", err)
	}
	return nil
}

// PruneVersions keeps the box's newest keep versions replaced after since.
func (r *Repo) PruneVersions(ctx context.Context, tenantID, boxID string, keep int, since time.Time) error {
	if _, err := r.db(ctx).Exec(ctx, `
DELETE FROM hub_backup_versions WHERE tenant_id = $1 AND box_id = $2 AND (
  replaced_at < $3 OR id NOT IN (
    SELECT id FROM hub_backup_versions WHERE tenant_id = $1 AND box_id = $2
    ORDER BY replaced_at DESC, id DESC LIMIT $4))`, tenantID, boxID, since, keep); err != nil {
		return fmt.Errorf("backups: prune versions: %w", err)
	}
	return nil
}
