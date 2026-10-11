package usecase

import (
	"context"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Members manages the people who pay and which devices are theirs.
type Members struct {
	repo  port.MemberRepository
	clock port.Clock
	log   port.Logger
}

// NewMembers wires the interactor.
func NewMembers(r port.MemberRepository, c port.Clock, l port.Logger) *Members {
	return &Members{repo: r, clock: c, log: l}
}

// List returns the router's members.
func (uc *Members) List(ctx context.Context, baseURL string) ([]domain.Member, error) {
	return uc.repo.List(ctx, RouterID(baseURL))
}

// Create adds a member.
func (uc *Members) Create(ctx context.Context, baseURL string, m domain.Member) (domain.Member, error) {
	if err := m.Validate(); err != nil {
		return domain.Member{}, err
	}
	m.ID, m.RouterID, m.CreatedAt, m.MACs = newID(), RouterID(baseURL), uc.clock.Now(), []domain.MAC{}
	if err := uc.repo.Create(ctx, m); err != nil {
		return domain.Member{}, err
	}
	uc.log.Infof("members: created %q (%s)", m.Name, m.Kind)
	return m, nil
}

// Update changes a member's details (not their devices).
func (uc *Members) Update(ctx context.Context, baseURL string, m domain.Member) (domain.Member, error) {
	if err := m.Validate(); err != nil {
		return domain.Member{}, err
	}
	m.RouterID = RouterID(baseURL)
	if err := uc.repo.Update(ctx, m); err != nil {
		return domain.Member{}, err
	}
	got, _, err := uc.repo.Get(ctx, m.RouterID, m.ID)
	return got, err
}

// Delete removes a member; their devices become unassigned.
func (uc *Members) Delete(ctx context.Context, baseURL, id string) error {
	if err := uc.repo.Delete(ctx, RouterID(baseURL), id); err != nil {
		return err
	}
	uc.log.Infof("members: deleted %s", id)
	return nil
}

// Assign gives a device to a member ("" unassigns it).
func (uc *Members) Assign(ctx context.Context, baseURL string, mac domain.MAC, memberID string) error {
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}
	if err := uc.repo.Assign(ctx, RouterID(baseURL), mac, memberID); err != nil {
		return err
	}
	uc.log.Infof("members: device %s assigned to %q", mac, memberID)
	return nil
}

// Siblings returns every MAC of the member owning mac, mac first; just mac
// when it belongs to nobody. Phones use a different random MAC per Wi-Fi
// network, so one person often appears as several devices: a cut must follow
// the person, not one of their addresses.
func (uc *Members) Siblings(ctx context.Context, baseURL string, mac domain.MAC) ([]domain.MAC, error) {
	ms, err := uc.repo.List(ctx, RouterID(baseURL))
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		for _, x := range m.MACs {
			if !x.Equal(mac) {
				continue
			}
			out := []domain.MAC{mac}
			for _, y := range m.MACs {
				if !y.Equal(mac) {
					out = append(out, y)
				}
			}
			return out, nil
		}
	}
	return []domain.MAC{mac}, nil
}
