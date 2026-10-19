package replyblock

import (
	"context"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Status is where one answered device is in its short life.
type Status string

const (
	// StatusQueued has been signed and handed to the queue.
	StatusQueued Status = "queued"
	// StatusPending was queued but no relay answered in time.
	StatusPending Status = "pending"
	// StatusDone was applied on the box.
	StatusDone Status = "done"
	// StatusRefused was turned down by the relay or the box.
	StatusRefused Status = "refused"
	// StatusFailed never reached a relay.
	StatusFailed Status = "failed"
)

// Action is one device of one alert, answered once.
type Action struct {
	TenantID  string
	AlertID   string
	ItemNo    int
	MAC       string
	UserID    string
	CommandID string
	Status    Status
	Reason    string
	At        time.Time
}

// Repo stores what replies asked for. It is also the hub's rate limit: the
// count of recent rows is what the limit is read from, because the relay's
// own counter lives in memory and starts again at every relay hand-over.
type Repo struct{ pool *pgxpool.Pool }

// NewRepo wires the repository.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) db(ctx context.Context) txctx.DBTX { return txctx.DB(ctx, r.pool) }

// Claim records a reply, once per (alert, device number). It reports
// whether this call is the one that claimed it: a provider that delivers
// the same message twice then blocks once.
func (r *Repo) Claim(ctx context.Context, a Action) (bool, error) {
	tag, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_reply_actions
		   (id, tenant_id, alert_id, item_no, mac, user_id, command_id, status, reason, at)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, '', $7, '', $8)
		 ON CONFLICT (alert_id, item_no) DO NOTHING`,
		uuid.NewString(), a.TenantID, a.AlertID, a.ItemNo, a.MAC, a.UserID, string(a.Status), a.At)
	if err != nil {
		return false, fmt.Errorf("replyblock: claim: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Finish records what became of a claimed reply.
func (r *Repo) Finish(ctx context.Context, tenantID, alertID string, itemNo int, status Status, commandID, reason string, at time.Time) error {
	_, err := r.db(ctx).Exec(ctx,
		`UPDATE hub_reply_actions
		 SET status = $4, command_id = $5, reason = $6, at = $7
		 WHERE tenant_id = $1 AND alert_id = $2 AND item_no = $3`,
		tenantID, alertID, itemNo, string(status), commandID, reason, at)
	if err != nil {
		return fmt.Errorf("replyblock: finish: %w", err)
	}
	return nil
}

// CountSince is the household's recent use of the narrow key. A reply the
// hub refused outright is not counted: it never produced a command, and a
// household must not lose its budget to a mistyped number.
func (r *Repo) CountSince(ctx context.Context, tenantID string, since time.Time) (int, error) {
	var n int
	err := r.db(ctx).QueryRow(ctx,
		`SELECT count(*) FROM hub_reply_actions
		 WHERE tenant_id = $1 AND at >= $2 AND status <> $3`,
		tenantID, since, string(StatusFailed)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("replyblock: count: %w", err)
	}
	return n, nil
}
