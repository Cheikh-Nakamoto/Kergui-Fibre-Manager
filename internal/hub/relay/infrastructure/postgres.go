// Package infrastructure implements the relay module's repositories on
// Postgres: the lease, the presences, the command queue and the snapshots.
//
// Two queries carry the correctness of the whole module. Heartbeat settles
// the lease under a row lock, so two relays cannot both be told they are
// active. Claim hands out a command with FOR UPDATE SKIP LOCKED, so two hub
// instances cannot hand out the same one.
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	agents "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay/domain"
)

// Repo implements the relay repositories.
type Repo struct{ pool *pgxpool.Pool }

var (
	_ domain.LeaseRepository    = (*Repo)(nil)
	_ domain.CommandRepository  = (*Repo)(nil)
	_ domain.SnapshotRepository = (*Repo)(nil)
)

// New wires the repositories.
func New(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// --- lease ----------------------------------------------------------------

// Heartbeat records a presence and settles the lease in one transaction.
//
// The lease row is locked first, so every concurrent heartbeat of the same
// household queues behind it: whatever the decision function says, only one
// relay can come out active.
func (r *Repo) Heartbeat(ctx context.Context, tenantID string, me domain.Presence, rep domain.Report, now time.Time,
	decide func(cur domain.Lease, me domain.Presence, others []domain.Presence) domain.Outcome,
) (domain.Outcome, error) {
	var out domain.Outcome
	err := txctx.ExecInTx(ctx, r.pool, func(ctx context.Context) error {
		db := r.db(ctx)
		// Create the household's lease row on first contact, then lock it.
		if _, err := db.Exec(ctx,
			`INSERT INTO hub_relay_leases (tenant_id) VALUES ($1) ON CONFLICT (tenant_id) DO NOTHING`, tenantID); err != nil {
			return fmt.Errorf("relay: lease row: %w", err)
		}
		var (
			cur     domain.Lease
			agentID *string
		)
		cur.TenantID = tenantID
		if err := db.QueryRow(ctx,
			`SELECT agent_id, epoch, expires_at FROM hub_relay_leases WHERE tenant_id = $1 FOR UPDATE`,
			tenantID).Scan(&agentID, &cur.Epoch, &cur.ExpiresAt); err != nil {
			return fmt.Errorf("relay: lock lease: %w", err)
		}
		if agentID != nil {
			cur.AgentID = *agentID
		}

		if err := r.savePresence(ctx, tenantID, me, rep, now); err != nil {
			return err
		}
		others, err := r.otherPresences(ctx, tenantID, me.AgentID)
		if err != nil {
			return err
		}

		out = decide(cur, me, others)
		switch {
		case out.Release:
			_, err = db.Exec(ctx,
				`UPDATE hub_relay_leases SET agent_id = NULL, expires_at = now(), updated_at = now()
				 WHERE tenant_id = $1 AND agent_id = $2`, tenantID, me.AgentID)
		case out.Grant || out.State == domain.StateActive:
			_, err = db.Exec(ctx,
				`UPDATE hub_relay_leases SET agent_id = $2, epoch = $3, expires_at = $4, updated_at = now()
				 WHERE tenant_id = $1`, tenantID, me.AgentID, out.Epoch, out.ExpiresAt)
		}
		return err
	})
	if err != nil {
		return domain.Outcome{}, err
	}
	return out, nil
}

func (r *Repo) savePresence(ctx context.Context, tenantID string, p domain.Presence, rep domain.Report, now time.Time) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_relay_presence (agent_id, tenant_id, kind, is_owner, eligible, reason, ssid, bssid, router_pin, lan_ip, version, seen_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (agent_id) DO UPDATE SET
		   kind = EXCLUDED.kind, is_owner = EXCLUDED.is_owner,
		   eligible = EXCLUDED.eligible, reason = EXCLUDED.reason, ssid = EXCLUDED.ssid,
		   bssid = EXCLUDED.bssid, router_pin = EXCLUDED.router_pin, lan_ip = EXCLUDED.lan_ip,
		   version = EXCLUDED.version, seen_at = EXCLUDED.seen_at`,
		p.AgentID, tenantID, string(p.Kind), p.IsOwner, p.Eligible, p.Reason,
		rep.SSID, rep.BSSID, rep.RouterPin, rep.LANIP, rep.Version, now)
	if err != nil {
		return fmt.Errorf("relay: presence: %w", err)
	}
	return nil
}

// otherPresences reads the household's other candidates. A revoked agent is
// left out: its token stops working, but a presence it wrote before being
// revoked must not keep a live relay from taking the lease.
func (r *Repo) otherPresences(ctx context.Context, tenantID, exceptAgentID string) ([]domain.Presence, error) {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT p.agent_id, p.kind, p.is_owner, p.eligible, p.reason, p.seen_at
		 FROM hub_relay_presence p
		 JOIN hub_agents a ON a.id = p.agent_id AND a.revoked_at IS NULL
		 WHERE p.tenant_id = $1 AND p.agent_id <> $2`,
		tenantID, exceptAgentID)
	if err != nil {
		return nil, fmt.Errorf("relay: presences: %w", err)
	}
	defer rows.Close()
	var out []domain.Presence
	for rows.Next() {
		var (
			p    domain.Presence
			kind string
		)
		if err := rows.Scan(&p.AgentID, &kind, &p.IsOwner, &p.Eligible, &p.Reason, &p.SeenAt); err != nil {
			return nil, fmt.Errorf("relay: presences scan: %w", err)
		}
		p.Kind = agents.Kind(kind)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Current returns the lease in force.
func (r *Repo) Current(ctx context.Context, tenantID string) (domain.Lease, error) {
	l := domain.Lease{TenantID: tenantID}
	var agentID *string
	err := r.db(ctx).QueryRow(ctx,
		`SELECT agent_id, epoch, expires_at FROM hub_relay_leases WHERE tenant_id = $1`,
		tenantID).Scan(&agentID, &l.Epoch, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, nil
	}
	if err != nil {
		return domain.Lease{}, fmt.Errorf("relay: read lease: %w", err)
	}
	if agentID != nil {
		l.AgentID = *agentID
	}
	return l, nil
}

// --- commands -------------------------------------------------------------

// Enqueue stores a signed command for the household's relay to pick up.
func (r *Repo) Enqueue(ctx context.Context, c *domain.Command) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_relay_commands (id, tenant_id, issuer_user_id, envelope, body, method, path, status, created_at, expires_at)
		 VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (id) DO NOTHING`,
		c.ID, c.TenantID, c.IssuerUserID, []byte(c.Envelope), c.Body, c.Method, c.Path,
		string(domain.StatusQueued), c.CreatedAt, c.ExpiresAt)
	if err != nil {
		return fmt.Errorf("relay: enqueue: %w", err)
	}
	return nil
}

// Claim hands the oldest due command to the lease holder.
//
// SKIP LOCKED is what makes this safe with several hub instances: a row
// another instance is handing out is passed over instead of waited on.
func (r *Repo) Claim(ctx context.Context, tenantID, agentID string, epoch int64, now time.Time) (*domain.Command, error) {
	var (
		c        domain.Command
		envelope []byte
		status   string
	)
	err := r.db(ctx).QueryRow(ctx,
		`UPDATE hub_relay_commands SET status = $4, agent_id = $5, epoch = $6, delivered_at = $3
		 WHERE id = (
		     SELECT id FROM hub_relay_commands
		     WHERE tenant_id = $1 AND status = $2 AND expires_at > $3
		     ORDER BY created_at
		     FOR UPDATE SKIP LOCKED
		     LIMIT 1
		 )
		 RETURNING id, tenant_id, envelope, body, method, path, status, epoch, created_at, expires_at`,
		tenantID, string(domain.StatusQueued), now, string(domain.StatusDelivered), agentID, epoch).
		Scan(&c.ID, &c.TenantID, &envelope, &c.Body, &c.Method, &c.Path, &status, &c.Epoch, &c.CreatedAt, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNoCommand
	}
	if err != nil {
		return nil, fmt.Errorf("relay: claim: %w", err)
	}
	c.Envelope = envelope
	c.Status = domain.Status(status)
	c.AgentID = agentID
	return &c, nil
}

// Complete records a relay's signed result.
//
// The epoch is part of the WHERE clause: a relay that lost the lease and
// came back cannot report on a command the new holder now owns.
func (r *Repo) Complete(ctx context.Context, tenantID, id, agentID string, epoch int64, result json.RawMessage, now time.Time) error {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_relay_commands SET status = $5, result = $6, done_at = $7
		 WHERE tenant_id = $1 AND id = $2 AND agent_id = $3 AND epoch = $4 AND status = $8`,
		tenantID, id, agentID, epoch, string(domain.StatusDone), []byte(result), now, string(domain.StatusDelivered))
	if err != nil {
		return fmt.Errorf("relay: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrStaleEpoch
	}
	return nil
}

// ByID returns one command of a household.
func (r *Repo) ByID(ctx context.Context, tenantID, id string) (*domain.Command, error) {
	var (
		c                domain.Command
		envelope, result []byte
		status           string
		agentID          *string
	)
	err := r.db(ctx).QueryRow(ctx,
		`SELECT id, tenant_id, envelope, body, method, path, status, epoch, agent_id, result, created_at, expires_at, delivered_at, done_at
		 FROM hub_relay_commands WHERE tenant_id = $1 AND id = $2`, tenantID, id).
		Scan(&c.ID, &c.TenantID, &envelope, &c.Body, &c.Method, &c.Path, &status, &c.Epoch, &agentID, &result,
			&c.CreatedAt, &c.ExpiresAt, &c.DeliveredAt, &c.DoneAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNoCommand
	}
	if err != nil {
		return nil, fmt.Errorf("relay: read command: %w", err)
	}
	c.Envelope, c.Result, c.Status = envelope, result, domain.Status(status)
	if agentID != nil {
		c.AgentID = *agentID
	}
	return &c, nil
}

// Expire marks commands nobody picked up in time, so the app can say why
// nothing happened rather than waiting forever.
func (r *Repo) Expire(ctx context.Context, now time.Time) (int, error) {
	tag, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_relay_commands SET status = $1, done_at = $2
		 WHERE status IN ($3, $4) AND expires_at <= $2`,
		string(domain.StatusExpired), now, string(domain.StatusQueued), string(domain.StatusDelivered))
	if err != nil {
		return 0, fmt.Errorf("relay: expire: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// --- snapshots ------------------------------------------------------------

// Put stores the newest snapshot of a kind, replacing the previous one. An
// older one that arrives late is dropped: as_of only moves forward.
func (r *Repo) Put(ctx context.Context, s domain.Snapshot) error {
	_, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_relay_snapshots (tenant_id, kind, agent_id, payload, as_of)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (tenant_id, kind) DO UPDATE
		 SET agent_id = EXCLUDED.agent_id, payload = EXCLUDED.payload, as_of = EXCLUDED.as_of
		 WHERE hub_relay_snapshots.as_of < EXCLUDED.as_of`,
		s.TenantID, s.Kind, s.AgentID, []byte(s.Payload), s.AsOf)
	if err != nil {
		return fmt.Errorf("relay: put snapshot: %w", err)
	}
	return nil
}

// Latest returns the newest snapshot of a kind.
func (r *Repo) Latest(ctx context.Context, tenantID, kind string) (domain.Snapshot, error) {
	var (
		s       domain.Snapshot
		payload []byte
	)
	err := r.db(ctx).QueryRow(ctx,
		`SELECT tenant_id, kind, agent_id, payload, as_of FROM hub_relay_snapshots
		 WHERE tenant_id = $1 AND kind = $2`, tenantID, kind).
		Scan(&s.TenantID, &s.Kind, &s.AgentID, &payload, &s.AsOf)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Snapshot{}, domain.ErrNoSnapshot
	}
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("relay: read snapshot: %w", err)
	}
	s.Payload = payload
	return s, nil
}
