package infrastructure

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

// Trial marks (owner decision, 2026-11-24): deleting an account erases the
// number and the box, but keeps a keyed digest of each in
// hub_licensing_trial_marks, so a re-created account cannot take the 7-day
// trial again. Only the digest is kept: without the hub's key it names no
// number and no box.

// SetTrialMarkKey sets the HMAC key of the trial marks. It must stay the
// same across deploys, or marks written before are no longer recognised.
// Without a key the digests are plain SHA-256 (tests, a hub without secret).
func (r *Repo) SetTrialMarkKey(k []byte) { r.markKey = append([]byte(nil), k...) }

func (r *Repo) trialDigest(kind, value string) []byte {
	if len(r.markKey) == 0 {
		d := sha256.Sum256([]byte("kergui.trial-mark.v1:" + kind + ":" + value))
		return d[:]
	}
	m := hmac.New(sha256.New, r.markKey)
	m.Write([]byte("kergui.trial-mark.v1:" + kind + ":" + value))
	return m.Sum(nil)
}

// RememberTrials marks the number and box of the account's trial, before
// the account (and, by cascade, the trial row) is deleted. It joins the
// caller's transaction. An account without a trial marks nothing.
func (r *Repo) RememberTrials(ctx context.Context, accountID string) error {
	rows, err := r.db(ctx).Query(ctx,
		`SELECT phone, COALESCE(box_id, '') FROM hub_licensing_trials WHERE account_id = $1`, accountID)
	if err != nil {
		return fmt.Errorf("licensing: read the trial to mark: %w", err)
	}
	type trial struct{ phone, box string }
	var list []trial
	for rows.Next() {
		var t trial
		if err := rows.Scan(&t.phone, &t.box); err != nil {
			rows.Close()
			return fmt.Errorf("licensing: read the trial to mark: %w", err)
		}
		list = append(list, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("licensing: read the trial to mark: %w", err)
	}
	for _, t := range list {
		if err := r.mark(ctx, "phone", t.phone); err != nil {
			return err
		}
		if t.box != "" {
			if err := r.mark(ctx, "box", t.box); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repo) mark(ctx context.Context, kind, value string) error {
	if _, err := r.db(ctx).Exec(ctx,
		`INSERT INTO hub_licensing_trial_marks (kind, digest) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		kind, r.trialDigest(kind, value)); err != nil {
		return fmt.Errorf("licensing: mark the trial's %s: %w", kind, err)
	}
	return nil
}
