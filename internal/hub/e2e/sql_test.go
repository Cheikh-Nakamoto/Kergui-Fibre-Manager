package e2e

import (
	"context"
	"os"
	"testing"

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
