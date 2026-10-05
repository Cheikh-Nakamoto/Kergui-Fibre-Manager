package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// RouterRepo persists managed routers (never their credentials).
type RouterRepo struct{ db *sql.DB }

var _ port.RouterRepository = (*RouterRepo)(nil)

// NewRouterRepo wires the repository.
func NewRouterRepo(db *DB) *RouterRepo { return &RouterRepo{db: db.SQL()} }

// Upsert inserts or updates a router.
func (r *RouterRepo) Upsert(ctx context.Context, rt domain.Router) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO routers (id, name, base_url, adapter_id, vendor, model, created_at)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, base_url=excluded.base_url, adapter_id=excluded.adapter_id,
  vendor=excluded.vendor, model=excluded.model`,
		rt.ID, rt.Name, rt.BaseURL, rt.AdapterID, rt.Vendor, rt.Model, unixOrZero(rt.CreatedAt))
	return err
}

// Get returns one router.
func (r *RouterRepo) Get(ctx context.Context, id string) (domain.Router, bool, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, name, base_url, adapter_id, vendor, model, created_at FROM routers WHERE id=?`, id)
	rt, err := scanRouter(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Router{}, false, nil
	}
	if err != nil {
		return domain.Router{}, false, err
	}
	return rt, true, nil
}

// List returns all routers ordered by id.
func (r *RouterRepo) List(ctx context.Context) ([]domain.Router, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id, name, base_url, adapter_id, vendor, model, created_at FROM routers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Router
	for rows.Next() {
		rt, err := scanRouter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

func scanRouter(s scanner) (domain.Router, error) {
	var (
		id, name, baseURL, adapterID, vendor, model string
		createdAt                                   int64
	)
	if err := s.Scan(&id, &name, &baseURL, &adapterID, &vendor, &model, &createdAt); err != nil {
		return domain.Router{}, err
	}
	return domain.Router{
		ID:        id,
		Name:      name,
		BaseURL:   baseURL,
		AdapterID: adapterID,
		Vendor:    vendor,
		Model:     model,
		CreatedAt: timeOrZero(createdAt),
	}, nil
}
