package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Identity follows devices across random MAC changes. A device that keeps
// coming back under new addresses escapes metering and cuts; after
// domain.IdentityLossLimit changes in domain.IdentityLossWindow it is
// blacklisted: every address it uses has its internet cut until the admin
// lifts the blacklist.
type Identity struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	devices port.DeviceRepository
	members port.MemberRepository
	repo    port.IdentityRepository
	cuts    port.CutRepository
	clock   port.Clock
	log     port.Logger
}

// NewIdentity wires the interactor.
func NewIdentity(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, devs port.DeviceRepository,
	m port.MemberRepository, r port.IdentityRepository, c port.CutRepository, clk port.Clock, l port.Logger) *Identity {
	return &Identity{factory: f, vault: v, disco: d, devices: devs, members: m, repo: r, cuts: c, clock: clk, log: l}
}

// IdentityInfo is what the dashboard shows for one device.
type IdentityInfo struct {
	Identity    string `json:"identity,omitempty"`
	Random      bool   `json:"random"`
	Losses      int    `json:"losses"`
	Blacklisted bool   `json:"blacklisted"`
	Shared      bool   `json:"shared"`
}

// coPresence is how close two last-seen times must be to count as "online
// together". One refresh stamps all its devices with the same instant, so
// only addresses listed by the same refresh match; a phone that switched
// network between two refreshes does not.
const coPresence = 5 * time.Second

type identityView struct {
	groups    map[string][]domain.Device
	states    map[string]domain.IdentityState
	protected map[domain.MAC]bool
	now       time.Time
}

func (uc *Identity) view(ctx context.Context, routerID string) (identityView, error) {
	devs, err := uc.devices.ListByRouter(ctx, routerID)
	if err != nil {
		return identityView{}, err
	}
	ms, err := uc.members.List(ctx, routerID)
	if err != nil {
		return identityView{}, err
	}
	states, err := uc.repo.Identities(ctx, routerID)
	if err != nil {
		return identityView{}, err
	}
	v := identityView{groups: map[string][]domain.Device{}, states: states, protected: map[domain.MAC]bool{}, now: uc.clock.Now()}
	for _, m := range ms {
		if m.Protected {
			for _, mac := range m.MACs {
				v.protected[mac] = true
			}
		}
	}
	for _, d := range devs {
		if v.protected[d.MAC] {
			continue
		}
		if key, ok := domain.IdentityKey(d.Hostname); ok {
			v.groups[key] = append(v.groups[key], d)
		}
	}
	return v, nil
}

func (v identityView) losses(id string) int {
	from := v.now.Add(-domain.IdentityLossWindow)
	if st := v.states[id]; st.LiftedAt.After(from) {
		from = st.LiftedAt
	}
	g := v.groups[id]
	s := make([]domain.Sighting, 0, len(g))
	for _, d := range g {
		s = append(s, domain.Sighting{MAC: d.MAC, FirstSeen: d.FirstSeen})
	}
	return domain.IdentityLosses(s, from)
}

// onlineTogether reports whether two addresses of a group were seen in the
// same refresh: then they are two devices that happen to share a name.
func onlineTogether(g []domain.Device) bool {
	for i := range g {
		for j := i + 1; j < len(g); j++ {
			if !g[i].Connected || !g[j].Connected {
				continue
			}
			d := g[i].LastSeen.Sub(g[j].LastSeen)
			if d < 0 {
				d = -d
			}
			if d <= coPresence && !g[i].LastSeen.IsZero() {
				return true
			}
		}
	}
	return false
}

// Info returns the identity status of every device that has one or uses a
// random address.
func (uc *Identity) Info(ctx context.Context, baseURL string) (map[domain.MAC]IdentityInfo, error) {
	v, err := uc.view(ctx, RouterID(baseURL))
	if err != nil {
		return nil, err
	}
	out := map[domain.MAC]IdentityInfo{}
	for id, g := range v.groups {
		st := v.states[id]
		info := IdentityInfo{Identity: id, Shared: st.Shared, Blacklisted: st.Blacklisted() && !st.Shared}
		if !st.Shared {
			info.Losses = v.losses(id)
		}
		for _, d := range g {
			i := info
			i.Random = d.MAC.IsRandomized()
			out[d.MAC] = i
		}
	}
	return out, nil
}

// IdentityReport says what a scan did.
type IdentityReport struct {
	Blacklisted []string     `json:"blacklisted"`
	Cut         []domain.MAC `json:"cut"`
}

// Scan updates shared flags, blacklists identities that reached the limit and
// cuts every address of blacklisted identities. The inventory must be fresh.
func (uc *Identity) Scan(ctx context.Context, in MACFilterInput) (IdentityReport, error) {
	routerID := RouterID(in.BaseURL)
	v, err := uc.view(ctx, routerID)
	if err != nil {
		return IdentityReport{}, err
	}
	rep := IdentityReport{Blacklisted: []string{}, Cut: []domain.MAC{}}
	blCuts, err := uc.repo.BlacklistCuts(ctx, routerID)
	if err != nil {
		return rep, err
	}
	type cut struct {
		mac domain.MAC
		id  string
	}
	var toCut []cut
	for id, g := range v.groups {
		st := v.states[id]
		if !st.Shared && onlineTogether(g) {
			if err := uc.repo.MarkShared(ctx, routerID, id); err != nil {
				return rep, err
			}
			st.Shared = true
			uc.log.Infof("identity: %q is used by several devices at once; it will never count as one device", id)
		}
		if st.Shared {
			continue
		}
		if !st.Blacklisted() {
			if n := v.losses(id); n >= domain.IdentityLossLimit {
				if err := uc.repo.SetBlacklisted(ctx, routerID, id, v.now); err != nil {
					return rep, err
				}
				st.BlacklistedAt = v.now
				rep.Blacklisted = append(rep.Blacklisted, id)
				uc.log.Warnf("identity: %q changed its Wi-Fi address %d times in 30 days: blacklisted", id, n)
			} else {
				continue
			}
		}
		for _, d := range g {
			if _, done := blCuts[d.MAC]; done || d.Blocked || v.now.Sub(d.LastSeen) > domain.IdentityLossWindow {
				continue // already cut (by us or for another reason), or long gone
			}
			toCut = append(toCut, cut{d.MAC, id})
		}
	}
	if len(toCut) == 0 {
		return rep, nil
	}
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return rep, err
	}
	if err := ensureFilterOn(ctx, gw, uc.log); err != nil {
		return rep, err
	}
	for _, c := range toCut {
		if err := gw.Block(ctx, c.mac); err != nil {
			uc.log.Errorf("identity: cannot cut %s of blacklisted %q: %v", c.mac, c.id, err)
			continue
		}
		if err := uc.repo.RecordBlacklistCut(ctx, routerID, c.mac, c.id); err != nil {
			return rep, err
		}
		rep.Cut = append(rep.Cut, c.mac)
		uc.log.Warnf("identity: internet cut for %s (blacklisted %q)", c.mac, c.id)
	}
	return rep, nil
}

// Lift ends a blacklist and restores the addresses it cut. Losses are counted
// afresh from now. Addresses also cut for an unpaid invoice stay cut.
func (uc *Identity) Lift(ctx context.Context, in MACFilterInput, identity string) ([]domain.MAC, error) {
	routerID := RouterID(in.BaseURL)
	states, err := uc.repo.Identities(ctx, routerID)
	if err != nil {
		return nil, err
	}
	if !states[identity].Blacklisted() {
		return nil, fmt.Errorf("%w: %q n'est pas blacklisté", domain.ErrInvalidInput, identity)
	}
	if err := uc.repo.Lift(ctx, routerID, identity, uc.clock.Now()); err != nil {
		return nil, err
	}
	blCuts, err := uc.repo.BlacklistCuts(ctx, routerID)
	if err != nil {
		return nil, err
	}
	unpaid, err := uc.cuts.Cuts(ctx, routerID)
	if err != nil {
		return nil, err
	}
	var mine []domain.MAC
	for mac, id := range blCuts {
		if id == identity {
			mine = append(mine, mac)
		}
	}
	restored := []domain.MAC{}
	if len(mine) == 0 {
		uc.log.Infof("identity: blacklist of %q lifted", identity)
		return restored, nil
	}
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	for _, mac := range mine {
		if _, owes := unpaid[mac]; !owes {
			if err := gw.Unblock(ctx, mac); err != nil {
				return restored, err
			}
			restored = append(restored, mac)
		}
		if err := uc.repo.RemoveBlacklistCut(ctx, routerID, mac); err != nil {
			return restored, err
		}
	}
	uc.log.Infof("identity: blacklist of %q lifted, %d address(es) restored", identity, len(restored))
	return restored, nil
}
