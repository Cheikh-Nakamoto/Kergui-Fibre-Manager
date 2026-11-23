package whatsmeow

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// TestOpen_OnTheHubsPostgres: the session tables are created in the hub's
// own database, an unlinked hub says so and refuses to send, and unlinking
// it is harmless. It needs make hub-db (KERGUI_HUB_TEST_DATABASE_URL).
func TestOpen_OnTheHubsPostgres(t *testing.T) {
	url := os.Getenv("KERGUI_HUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KERGUI_HUB_TEST_DATABASE_URL not set (make hub-test)")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	n, err := Open(ctx, pool, quiet())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var tables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name LIKE 'whatsmeow_%'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables == 0 {
		t.Fatal("no whatsmeow_* tables were created")
	}
	if st := n.Status(); st.Linked || st.Connected {
		t.Fatalf("a fresh session reads %+v, want unlinked", st)
	}
	if _, err := n.Send(ctx, "+221771234567", domain.OutboundMessage{Text: "x"}); !errors.Is(err, domain.ErrNotActivated) {
		t.Fatalf("Send unlinked: err = %v, want ErrNotActivated", err)
	}
	if err := n.Unlink(ctx); err != nil {
		t.Fatalf("Unlink an unlinked session: %v", err)
	}
	// Opening again reads the same (empty) store.
	if _, err := Open(ctx, pool, quiet()); err != nil {
		t.Fatalf("second Open: %v", err)
	}
}
