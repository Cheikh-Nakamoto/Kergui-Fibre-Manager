package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

type memIdentity struct {
	states map[string]domain.IdentityState
	cuts   map[domain.MAC]string
}

func newMemIdentity() *memIdentity {
	return &memIdentity{states: map[string]domain.IdentityState{}, cuts: map[domain.MAC]string{}}
}
func (m *memIdentity) Identities(context.Context, string) (map[string]domain.IdentityState, error) {
	out := map[string]domain.IdentityState{}
	for k, v := range m.states {
		out[k] = v
	}
	return out, nil
}
func (m *memIdentity) MarkShared(_ context.Context, _, id string) error {
	s := m.states[id]
	s.Shared = true
	m.states[id] = s
	return nil
}
func (m *memIdentity) SetBlacklisted(_ context.Context, _, id string, at time.Time) error {
	s := m.states[id]
	s.BlacklistedAt = at
	m.states[id] = s
	return nil
}
func (m *memIdentity) Lift(_ context.Context, _, id string, at time.Time) error {
	s := m.states[id]
	s.LiftedAt = at
	m.states[id] = s
	return nil
}
func (m *memIdentity) BlacklistCuts(context.Context, string) (map[domain.MAC]string, error) {
	out := map[domain.MAC]string{}
	for k, v := range m.cuts {
		out[k] = v
	}
	return out, nil
}
func (m *memIdentity) RecordBlacklistCut(_ context.Context, _ string, mac domain.MAC, id string) error {
	m.cuts[mac] = id
	return nil
}
func (m *memIdentity) RemoveBlacklistCut(_ context.Context, _ string, mac domain.MAC) error {
	delete(m.cuts, mac)
	return nil
}

type identityFixture struct {
	uc    *usecase.Identity
	devs  *listDevices
	repo  *memIdentity
	gw    *writeGateway
	now   *time.Time
	in    usecase.MACFilterInput
	route string
}

func newIdentityFixture(t *testing.T) identityFixture {
	t.Helper()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	routerID := usecase.RouterID("http://r")
	vault := newVault()
	_ = vault.Store(context.Background(), routerID, domain.Credentials{Username: "u", Password: "p"})
	f := identityFixture{
		devs: &listDevices{fakeDeviceRepo{byMAC: map[string]domain.Device{}}},
		repo: newMemIdentity(),
		gw:   &writeGateway{fakeGateway: fakeGateway{meta: f660Meta()}},
		now:  &now, in: usecase.MACFilterInput{BaseURL: "http://r", AdapterID: "zte_f660"}, route: routerID,
	}
	f.uc = usecase.NewIdentity(writeFactory{f.gw}, vault, fakeDisco{}, f.devs, &memMembers{}, f.repo,
		&memCuts{m: map[domain.MAC]string{}}, mutableClock{&now}, nopLogger{})
	return f
}

func (f identityFixture) see(mac, name string, firstDaysAgo int, lastSeen time.Time) {
	f.devs.byMAC[mac] = domain.Device{RouterID: f.route, MAC: domain.MustMAC(mac), Hostname: name,
		FirstSeen: f.now.AddDate(0, 0, -firstDaysAgo), LastSeen: lastSeen, Connected: true}
}

func TestIdentity_BlacklistsOnThirdChangeAndLifts(t *testing.T) {
	f := newIdentityFixture(t)
	ctx := context.Background()
	old := f.now.Add(-time.Hour)
	f.see("ba:00:00:00:00:01", "OPPO-Find-X5", 40, old) // the phone's first address
	f.see("9a:00:00:00:00:02", "OPPO-Find-X5", 20, old.Add(10*time.Minute))
	f.see("da:00:00:00:00:03", "OPPO-Find-X5", 10, old.Add(20*time.Minute))
	f.see("aa:00:00:00:00:09", "iPhone", 1, *f.now) // generic name: never tied to anything

	if rep, _ := f.uc.Scan(ctx, f.in); len(rep.Blacklisted) != 0 {
		t.Fatalf("2 changes must not blacklist: %+v", rep)
	}
	info, _ := f.uc.Info(ctx, "http://r")
	if i := info[domain.MustMAC("da:00:00:00:00:03")]; i.Losses != 2 || !i.Random || i.Blacklisted {
		t.Errorf("info = %+v", i)
	}
	if _, tied := info[domain.MustMAC("aa:00:00:00:00:09")]; tied {
		t.Error("an iPhone with a generic name must not get an identity")
	}

	f.see("fa:00:00:00:00:04", "OPPO-Find-X5", 1, *f.now) // third change
	rep, err := f.uc.Scan(ctx, f.in)
	if err != nil || len(rep.Blacklisted) != 1 || len(rep.Cut) != 4 || len(f.gw.blocked) != 4 {
		t.Fatalf("third change: %+v, %v, blocked %v", rep, err, f.gw.blocked)
	}
	if again, _ := f.uc.Scan(ctx, f.in); len(again.Cut) != 0 {
		t.Errorf("second scan cut again: %+v", again)
	}

	restored, err := f.uc.Lift(ctx, f.in, "oppo-find-x5")
	if err != nil || len(restored) != 4 || len(f.repo.cuts) != 0 {
		t.Fatalf("lift: %v, %v, cuts left %v", restored, err, f.repo.cuts)
	}
	if rep, _ := f.uc.Scan(ctx, f.in); len(rep.Blacklisted) != 0 {
		t.Error("after a lift, old changes must not blacklist again")
	}
}

func TestIdentity_SameNameOnlineTogetherIsTwoDevices(t *testing.T) {
	f := newIdentityFixture(t)
	ctx := context.Background()
	for i, mac := range []string{"ba:00:00:00:00:01", "9a:00:00:00:00:02", "da:00:00:00:00:03", "fa:00:00:00:00:04"} {
		f.see(mac, "Galaxy-A12", 20-i, *f.now) // all listed by the same refresh
	}
	rep, _ := f.uc.Scan(ctx, f.in)
	if len(rep.Blacklisted) != 0 || len(f.gw.blocked) != 0 || !f.repo.states["galaxy-a12"].Shared {
		t.Errorf("devices sharing a model name were blacklisted: %+v", rep)
	}
}
