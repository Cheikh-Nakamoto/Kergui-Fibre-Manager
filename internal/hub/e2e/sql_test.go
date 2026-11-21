package e2e

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// execSQL writes directly to the hub's database. The end-to-end test uses
// it only to forge what a compromised hub could write, which no API would
// accept.
func execSQL(t *testing.T, query string, args ...any) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// lease is the household's relay lease as the hub stored it.
type lease struct {
	holder string
	epoch  int64
}

// currentLease reads the household's lease. No API shows it to the app, and
// the failover test has to know which relay holds the box before it stops
// one. A lease past its expiry reads as nobody's.
func currentLease(t *testing.T, w *world) lease {
	t.Helper()
	if w.db == nil {
		pool, err := pgxpool.New(context.Background(), os.Getenv(testDatabaseEnv))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		w.db = pool
	}
	var (
		l      lease
		holder *string
	)
	err := w.db.QueryRow(context.Background(),
		`SELECT agent_id::text, epoch FROM hub_relay_leases WHERE tenant_id = $1 AND expires_at > now()`,
		w.owner.tenantID).Scan(&holder, &l.epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease{}
	}
	if err != nil {
		t.Fatal(err)
	}
	if holder != nil {
		l.holder = *holder
	}
	return l
}
