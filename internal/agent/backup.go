package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
)

// BackupSource reads the household's Kergui backup: the archive of
// `kergui export` (appareils.csv, conso.csv).
type BackupSource func(ctx context.Context) ([]byte, error)

// backupState paces the backup sent to the hub. It lives in memory, like the
// snapshots' pace: a relay that restarts sends once more, which the hub
// merges into the same backup.
type backupState struct {
	// next is the earliest time of the next try: a day after a send, the
	// next snapshot cycle after a failure worth retrying.
	next time.Time
}

// maybeBackup sends the household's backup to the hub with the snapshots, at
// most once a day (issue #70), so the names given on the PC reach the
// household's phones. A failure is logged and never holds the relay's work
// back: a hub that is down or refuses the token is tried again on the next
// snapshot cycle; an archive the hub refuses as such (too large, not a
// backup) waits for the next day, since the same archive would be refused
// again.
func (a *Agent) maybeBackup(ctx context.Context) {
	if a.cfg.Backup == nil || a.cfg.BoxID == "" || !a.active {
		return
	}
	now := a.now()
	if now.Before(a.backup.next) {
		return
	}
	err := a.sendBackup(ctx)
	var refused *hubStatusError
	switch {
	case err == nil:
		a.backup.next = now.Add(a.timings.BackupEvery)
	case errors.As(err, &refused) &&
		(refused.Code == http.StatusRequestEntityTooLarge || refused.Code == http.StatusBadRequest):
		a.log.Warnf("agent: backup refused by the hub, next try in %s: %v", a.timings.BackupEvery, err)
		a.backup.next = now.Add(a.timings.BackupEvery)
	default:
		a.log.Warnf("agent: backup: %v", err)
		a.backup.next = now.Add(a.timings.SnapshotEvery)
	}
}

// sendBackup reads the export and sends it. The archive is read and written
// again with the backup's columns only, so whatever else a file might carry
// (a password, the vault) never leaves the PC.
func (a *Agent) sendBackup(ctx context.Context) error {
	raw, err := a.cfg.Backup(ctx)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	parsed, err := backup.Read(raw)
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	archive, err := parsed.Bytes()
	if err != nil {
		return fmt.Errorf("export: %w", err)
	}
	return a.hub.putBackup(ctx, a.cfg.BoxID, archive)
}
