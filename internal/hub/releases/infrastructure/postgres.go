// Package infrastructure keeps the releases on Postgres. Queries go through
// txctx.DB so they join the transaction InTarget opens.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/usecase"
)

// Repo is the releases store.
type Repo struct{ pool *pgxpool.Pool }

var _ usecase.Store = (*Repo)(nil)

// New wires the store.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// InTarget takes a transaction-scoped advisory lock on the platform and
// channel, so two back-office writes or a write and a declaration take turns.
func (r *Repo) InTarget(ctx context.Context, t domain.Target, fn func(context.Context) error) error {
	return txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		if _, err := r.db(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('hub_releases/' || $1 || '/' || $2))`,
			string(t.Platform), string(t.Channel)); err != nil {
			return fmt.Errorf("releases: lock: %w", err)
		}
		return fn(ctx)
	})
}

const releaseColumns = `id::text, platform, channel, version_name, build, url, sha256, size, notes, state,
	declared_at, published_at, withdrawn_at`

func scan(row pgx.Row) (domain.Release, error) {
	var r domain.Release
	var platform, channel, state string
	err := row.Scan(&r.ID, &platform, &channel, &r.VersionName, &r.Build, &r.URL, &r.SHA256, &r.Size, &r.Notes, &state,
		&r.DeclaredAt, &r.PublishedAt, &r.WithdrawnAt)
	r.Platform, r.Channel, r.State = domain.Platform(platform), domain.Channel(channel), domain.State(state)
	return r, err
}

// List returns a target's releases, newest build first.
func (r *Repo) List(ctx context.Context, t domain.Target) ([]domain.Release, error) {
	rows, err := r.db(ctx).Query(ctx, `SELECT `+releaseColumns+` FROM hub_releases
		WHERE platform = $1 AND channel = $2 ORDER BY build DESC`, string(t.Platform), string(t.Channel))
	if err != nil {
		return nil, fmt.Errorf("releases: list: %w", err)
	}
	defer rows.Close()
	out := []domain.Release{}
	for rows.Next() {
		rel, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("releases: list: %w", err)
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

// Get returns one release.
func (r *Repo) Get(ctx context.Context, id string) (domain.Release, error) {
	rel, err := scan(r.db(ctx).QueryRow(ctx, `SELECT `+releaseColumns+` FROM hub_releases WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Release{}, usecase.ErrNotFound
	}
	if err != nil {
		return domain.Release{}, fmt.Errorf("releases: get: %w", err)
	}
	return rel, nil
}

// Insert adds a declared release.
func (r *Repo) Insert(ctx context.Context, rel domain.Release) error {
	_, err := r.db(ctx).Exec(ctx, `INSERT INTO hub_releases
		(id, platform, channel, version_name, build, url, sha256, size, notes, state, declared_at, published_at, withdrawn_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		rel.ID, string(rel.Platform), string(rel.Channel), rel.VersionName, rel.Build, rel.URL, rel.SHA256, rel.Size,
		rel.Notes, string(rel.State), rel.DeclaredAt, rel.PublishedAt, rel.WithdrawnAt)
	if err != nil {
		return fmt.Errorf("releases: insert: %w", err)
	}
	return nil
}

// Update writes a release's file, notes and state back.
func (r *Repo) Update(ctx context.Context, rel domain.Release) error {
	tag, err := r.db(ctx).Exec(ctx, `UPDATE hub_releases SET version_name = $2, url = $3, sha256 = $4, size = $5,
		notes = $6, state = $7, published_at = $8, withdrawn_at = $9 WHERE id = $1`,
		rel.ID, rel.VersionName, rel.URL, rel.SHA256, rel.Size, rel.Notes, string(rel.State), rel.PublishedAt, rel.WithdrawnAt)
	if err != nil {
		return fmt.Errorf("releases: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return usecase.ErrNotFound
	}
	return nil
}

// Minimum is the target's minimum build, 0 when none is set.
func (r *Repo) Minimum(ctx context.Context, t domain.Target) (int64, error) {
	var b int64
	err := r.db(ctx).QueryRow(ctx, `SELECT build FROM hub_release_minimums WHERE platform = $1 AND channel = $2`,
		string(t.Platform), string(t.Channel)).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("releases: minimum: %w", err)
	}
	return b, nil
}

// SetMinimum sets the target's minimum build.
func (r *Repo) SetMinimum(ctx context.Context, t domain.Target, build int64, now time.Time) error {
	_, err := r.db(ctx).Exec(ctx, `INSERT INTO hub_release_minimums (platform, channel, build, set_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (platform, channel) DO UPDATE SET build = EXCLUDED.build, set_at = EXCLUDED.set_at`,
		string(t.Platform), string(t.Channel), build, now)
	if err != nil {
		return fmt.Errorf("releases: set minimum: %w", err)
	}
	return nil
}

// SeeInstallation records the last build an installation reported.
func (r *Repo) SeeInstallation(ctx context.Context, t domain.Target, id string, build int64, now time.Time) error {
	_, err := r.db(ctx).Exec(ctx, `INSERT INTO hub_release_installs (platform, channel, install_id, build, seen_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (platform, channel, install_id) DO UPDATE SET build = EXCLUDED.build, seen_at = EXCLUDED.seen_at`,
		string(t.Platform), string(t.Channel), id, build, now)
	if err != nil {
		return fmt.Errorf("releases: see installation: %w", err)
	}
	return nil
}

// Installations counts the installations seen since, per build.
func (r *Repo) Installations(ctx context.Context, t domain.Target, since time.Time) (map[int64]int, error) {
	rows, err := r.db(ctx).Query(ctx, `SELECT build, count(*) FROM hub_release_installs
		WHERE platform = $1 AND channel = $2 AND seen_at > $3 GROUP BY build`,
		string(t.Platform), string(t.Channel), since)
	if err != nil {
		return nil, fmt.Errorf("releases: installations: %w", err)
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var b int64
		var n int
		if err := rows.Scan(&b, &n); err != nil {
			return nil, fmt.Errorf("releases: installations: %w", err)
		}
		out[b] = n
	}
	return out, rows.Err()
}

// ForgetInstallations drops the installations not seen since before.
func (r *Repo) ForgetInstallations(ctx context.Context, before time.Time) error {
	if _, err := r.db(ctx).Exec(ctx, `DELETE FROM hub_release_installs WHERE seen_at <= $1`, before); err != nil {
		return fmt.Errorf("releases: forget installations: %w", err)
	}
	return nil
}
