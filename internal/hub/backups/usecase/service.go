// Package usecase keeps a household's backups: a new archive is merged into
// the box's current one with the import's rules (internal/backup), and the
// archive it replaces is kept a short while.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/backups/domain"
)

// How long, and how many, replaced archives are kept: enough to undo a bad
// import or a wrong phone's backup, not a history.
const (
	KeepVersions   = 5
	KeepVersionFor = 7 * 24 * time.Hour
)

// Store is the module's persistence.
type Store interface {
	// InBox runs fn in a transaction that holds the box's lock, so two
	// sources sending at once merge one after the other.
	InBox(ctx context.Context, tenantID, boxID string, fn func(context.Context) error) error
	// Current is the box's backup, domain.ErrNotFound when none.
	Current(ctx context.Context, tenantID, boxID string) (domain.Backup, error)
	// List is the household's backups, without their archives.
	List(ctx context.Context, tenantID string) ([]domain.Backup, error)
	// Put stores b as the box's current backup.
	Put(ctx context.Context, b domain.Backup) error
	// KeepVersion files a replaced backup.
	KeepVersion(ctx context.Context, b domain.Backup, replacedAt time.Time) error
	// PruneVersions keeps the box's newest keep versions replaced after since.
	PruneVersions(ctx context.Context, tenantID, boxID string, keep int, since time.Time) error
}

// Saved is what a save did.
type Saved struct {
	domain.Backup
	// Merged is set when the box had a backup the new one was merged into.
	Merged bool
}

// Service is the module's use cases.
type Service struct {
	Store Store
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Save merges archive into the box's backup. The archive must be a Kergui
// backup (backup.ErrBadBackup otherwise); what is stored is rewritten from
// its rows, so nothing but the backup's columns reaches the hub's database.
func (s *Service) Save(ctx context.Context, tenantID, boxID string, src domain.Source, archive []byte) (Saved, error) {
	if !domain.ValidBox(boxID) {
		return Saved{}, domain.ErrBadBox
	}
	if len(archive) > domain.MaxArchive {
		return Saved{}, domain.ErrTooLarge
	}
	incoming, err := backup.Read(archive)
	if err != nil {
		return Saved{}, err
	}
	var out Saved
	err = s.Store.InBox(ctx, tenantID, boxID, func(ctx context.Context) error {
		now := s.now()
		merged := backup.Merge(backup.Archive{}, incoming)
		prev, err := s.Store.Current(ctx, tenantID, boxID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			return err
		default:
			stored, err := backup.Read(prev.Archive)
			if err != nil {
				return err
			}
			merged = backup.Merge(stored, incoming)
			out.Merged = true
		}
		raw, err := merged.Bytes()
		if err != nil {
			return err
		}
		if len(raw) > domain.MaxArchive {
			return domain.ErrTooLarge
		}
		b := domain.Backup{
			TenantID: tenantID, BoxID: boxID, Archive: raw, Source: src, UpdatedAt: now,
			Devices: len(merged.Devices), Months: months(merged),
		}
		if out.Merged {
			if err := s.Store.KeepVersion(ctx, prev, now); err != nil {
				return err
			}
			if err := s.Store.PruneVersions(ctx, tenantID, boxID, KeepVersions, now.Add(-KeepVersionFor)); err != nil {
				return err
			}
		}
		if err := s.Store.Put(ctx, b); err != nil {
			return err
		}
		out.Backup = b
		return nil
	})
	return out, err
}

// Get is the box's backup.
func (s *Service) Get(ctx context.Context, tenantID, boxID string) (domain.Backup, error) {
	if !domain.ValidBox(boxID) {
		return domain.Backup{}, domain.ErrBadBox
	}
	return s.Store.Current(ctx, tenantID, boxID)
}

// List is the household's backups, one per box, without their archives.
func (s *Service) List(ctx context.Context, tenantID string) ([]domain.Backup, error) {
	return s.Store.List(ctx, tenantID)
}

// months counts the distinct months the archive has figures for.
func months(a backup.Archive) int {
	seen := map[string]bool{}
	for _, u := range a.Usage {
		seen[u.Month] = true
	}
	return len(seen)
}
