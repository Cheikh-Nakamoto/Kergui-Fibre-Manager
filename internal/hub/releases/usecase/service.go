// Package usecase runs the releases module (issue #71): CI declares a
// build, the app checks for an update, and the back-office publishes,
// withdraws, writes the notes and sets the minimum.
package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/releases/domain"
)

// ErrNotFound is a release id that does not exist.
var ErrNotFound = errors.New("releases: not found")

// Store keeps the releases, the minimums and the installations seen.
type Store interface {
	// InTarget runs fn alone for its platform and channel: the rules that
	// read every release before writing one (the minimum, a withdrawal)
	// see no concurrent change.
	InTarget(ctx context.Context, t domain.Target, fn func(context.Context) error) error
	// List returns a target's releases, newest build first.
	List(ctx context.Context, t domain.Target) ([]domain.Release, error)
	Get(ctx context.Context, id string) (domain.Release, error)
	Insert(ctx context.Context, r domain.Release) error
	Update(ctx context.Context, r domain.Release) error
	// Minimum is 0 when none is set.
	Minimum(ctx context.Context, t domain.Target) (int64, error)
	SetMinimum(ctx context.Context, t domain.Target, build int64, now time.Time) error
	// SeeInstallation records the last build an installation reported.
	SeeInstallation(ctx context.Context, t domain.Target, id string, build int64, now time.Time) error
	// Installations counts the installations seen since, per build.
	Installations(ctx context.Context, t domain.Target, since time.Time) (map[int64]int, error)
	// ForgetInstallations drops what was seen before: nothing is kept past
	// the counting window.
	ForgetInstallations(ctx context.Context, before time.Time) error
}

// Service is the module's use cases.
type Service struct {
	Store Store
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

// Declare records a build CI built. Declaring a build again is harmless;
// created reports whether it is new.
func (s *Service) Declare(ctx context.Context, d domain.Declaration) (r domain.Release, created bool, err error) {
	d = d.Normalize()
	if err := d.Validate(); err != nil {
		return domain.Release{}, false, err
	}
	err = s.Store.InTarget(ctx, d.Target, func(ctx context.Context) error {
		all, err := s.Store.List(ctx, d.Target)
		if err != nil {
			return err
		}
		for _, known := range all {
			if known.Build != d.Build {
				continue
			}
			next, changed, err := known.Redeclare(d)
			if err != nil {
				return err
			}
			r = next
			if changed {
				return s.Store.Update(ctx, next)
			}
			return nil
		}
		r = domain.Release{ID: uuid.NewString(), Declaration: d, State: domain.Pending, DeclaredAt: s.now()}
		created = true
		return s.Store.Insert(ctx, r)
	})
	return r, created, err
}

// CheckInput is what the app sends.
type CheckInput struct {
	domain.Target
	Build          int64
	InstallationID string
}

// CheckResult is the hub's answer: the update offered, if any, and the
// minimum build; Required is the caller being below it.
type CheckResult struct {
	Update   *domain.Release
	Minimum  int64
	Required bool
}

// Check answers an app and remembers the build it runs, for the counts.
func (s *Service) Check(ctx context.Context, in CheckInput) (CheckResult, error) {
	if err := in.Target.Validate(); err != nil {
		return CheckResult{}, err
	}
	if err := domain.ValidateBuild(in.Build); err != nil {
		return CheckResult{}, err
	}
	if err := domain.ValidateInstallationID(in.InstallationID); err != nil {
		return CheckResult{}, err
	}
	all, err := s.Store.List(ctx, in.Target)
	if err != nil {
		return CheckResult{}, err
	}
	minimum, err := s.Store.Minimum(ctx, in.Target)
	if err != nil {
		return CheckResult{}, err
	}
	if err := s.Store.SeeInstallation(ctx, in.Target, in.InstallationID, in.Build, s.now()); err != nil {
		return CheckResult{}, err
	}
	return CheckResult{Update: domain.Offer(all, in.Build), Minimum: minimum, Required: in.Build < minimum}, nil
}

// Latest is the newest published release of a target, for the website's
// download link: a pending or withdrawn build is never handed out. nil when
// none is published. Unlike Check, it remembers nothing.
func (s *Service) Latest(ctx context.Context, t domain.Target) (*domain.Release, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	all, err := s.Store.List(ctx, t)
	if err != nil {
		return nil, err
	}
	return domain.Offer(all, 0), nil
}

// Listing is the back-office's view of a target.
type Listing struct {
	Target   domain.Target
	Releases []domain.Release
	Minimum  int64
	// Installs counts the installations seen in the window, per build,
	// declared or not.
	Installs map[int64]int
}

// List returns a target's releases, minimum and counts.
func (s *Service) List(ctx context.Context, t domain.Target) (Listing, error) {
	if err := t.Validate(); err != nil {
		return Listing{}, err
	}
	since := s.now().Add(-domain.CountWindow)
	if err := s.Store.ForgetInstallations(ctx, since); err != nil {
		return Listing{}, err
	}
	all, err := s.Store.List(ctx, t)
	if err != nil {
		return Listing{}, err
	}
	minimum, err := s.Store.Minimum(ctx, t)
	if err != nil {
		return Listing{}, err
	}
	counts, err := s.Store.Installations(ctx, t, since)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Target: t, Releases: all, Minimum: minimum, Installs: counts}, nil
}

// Publish offers a release to the phones.
func (s *Service) Publish(ctx context.Context, id string) (domain.Release, error) {
	return s.change(ctx, id, func(_ []domain.Release, _ int64, r domain.Release) (domain.Release, error) {
		return r.Publish(s.now())
	})
}

// Withdraw stops offering a release; the previous published one is offered
// again. The build the minimum stands on stays.
func (s *Service) Withdraw(ctx context.Context, id string) (domain.Release, error) {
	return s.change(ctx, id, func(all []domain.Release, minimum int64, r domain.Release) (domain.Release, error) {
		next, err := r.Withdraw(s.now())
		if err != nil {
			return r, err
		}
		if !domain.Covered(all, minimum, r.ID) {
			return r, domain.ErrMinimumUncovered
		}
		return next, nil
	})
}

// EditNotes replaces a release's notes.
func (s *Service) EditNotes(ctx context.Context, id, notes string) (domain.Release, error) {
	if err := domain.ValidateNotes(notes); err != nil {
		return domain.Release{}, err
	}
	return s.change(ctx, id, func(_ []domain.Release, _ int64, r domain.Release) (domain.Release, error) {
		r.Notes = notes
		return r, nil
	})
}

// SetMinimum sets the build below which the app blocks; 0 clears it. It
// must be a published build.
func (s *Service) SetMinimum(ctx context.Context, t domain.Target, build int64) error {
	if err := t.Validate(); err != nil {
		return err
	}
	return s.Store.InTarget(ctx, t, func(ctx context.Context) error {
		all, err := s.Store.List(ctx, t)
		if err != nil {
			return err
		}
		if err := domain.CheckMinimum(all, build); err != nil {
			return err
		}
		return s.Store.SetMinimum(ctx, t, build, s.now())
	})
}

// change applies fn to a release, with its target's releases and minimum
// read in the same lock.
func (s *Service) change(ctx context.Context, id string,
	fn func(all []domain.Release, minimum int64, r domain.Release) (domain.Release, error)) (domain.Release, error) {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Release{}, ErrNotFound
	}
	found, err := s.Store.Get(ctx, id)
	if err != nil {
		return domain.Release{}, err
	}
	var out domain.Release
	err = s.Store.InTarget(ctx, found.Target, func(ctx context.Context) error {
		r, err := s.Store.Get(ctx, id)
		if err != nil {
			return err
		}
		all, err := s.Store.List(ctx, r.Target)
		if err != nil {
			return err
		}
		minimum, err := s.Store.Minimum(ctx, r.Target)
		if err != nil {
			return err
		}
		if out, err = fn(all, minimum, r); err != nil {
			return err
		}
		return s.Store.Update(ctx, out)
	})
	return out, err
}
