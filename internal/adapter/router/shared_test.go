package router

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

type countingGateway struct {
	logins, inside, maxInside atomic.Int32
	expireNext                atomic.Bool
}

func (g *countingGateway) Login(context.Context, domain.Credentials) error {
	g.logins.Add(1)
	return nil
}
func (g *countingGateway) Devices(context.Context) ([]domain.Device, error) {
	n := g.inside.Add(1)
	defer g.inside.Add(-1)
	for {
		m := g.maxInside.Load()
		if n <= m || g.maxInside.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	if g.expireNext.CompareAndSwap(true, false) {
		return nil, domain.ErrSessionExpired
	}
	return []domain.Device{{}}, nil
}
func (g *countingGateway) RouterInfo(context.Context) (*domain.RouterInfo, error)   { return nil, nil }
func (g *countingGateway) AccessRules(context.Context) ([]domain.AccessRule, error) { return nil, nil }
func (g *countingGateway) Block(context.Context, domain.MAC) error                  { return nil }
func (g *countingGateway) Unblock(context.Context, domain.MAC) error                { return nil }
func (g *countingGateway) Meta() domain.AdapterMeta                                 { return domain.AdapterMeta{ID: "fake"} }

func sharedWith(inner port.RouterPort) *SharedFactory {
	f := NewFactory()
	f.Register(domain.AdapterMeta{ID: "fake"}, func(string, port.RouterOptions) (port.RouterPort, error) { return inner, nil })
	return NewSharedFactory(f)
}

// TestShared_PinIsPartOfTheKey: the pinned fingerprint is checked by the
// session's TLS transport, so two different pins must not share a session —
// the second caller would unknowingly keep verifying the first pin.
func TestShared_PinIsPartOfTheKey(t *testing.T) {
	var built int
	f := NewFactory()
	f.Register(domain.AdapterMeta{ID: "fake"}, func(string, port.RouterOptions) (port.RouterPort, error) {
		built++
		return &countingGateway{}, nil
	})
	sf := NewSharedFactory(f)

	const url = "https://192.168.1.1"
	if _, err := sf.New("fake", url, port.RouterOptions{PinSHA256: "aa"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sf.New("fake", url, port.RouterOptions{PinSHA256: "aa"}); err != nil {
		t.Fatal(err)
	}
	if built != 1 {
		t.Fatalf("same pin built %d sessions, want 1", built)
	}
	if _, err := sf.New("fake", url, port.RouterOptions{PinSHA256: "bb"}); err != nil {
		t.Fatal(err)
	}
	if built != 2 {
		t.Errorf("a changed pin reused the old session (built=%d)", built)
	}
}

func TestShared_OneLoginAndNoOverlap(t *testing.T) {
	inner := &countingGateway{}
	f := sharedWith(inner)
	creds := domain.Credentials{Username: "u", Password: "p"}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gw, err := f.New("fake", "https://r", port.RouterOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if err := gw.Login(ctx, creds); err != nil {
				t.Error(err)
			}
			if _, err := gw.Devices(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := inner.logins.Load(); n != 1 {
		t.Errorf("logins = %d, want 1 for 20 concurrent requests", n)
	}
	if m := inner.maxInside.Load(); m != 1 {
		t.Errorf("max concurrent router calls = %d, want 1", m)
	}
}

func TestShared_RelogsInOnceWhenSessionEnds(t *testing.T) {
	inner := &countingGateway{}
	gw, _ := sharedWith(inner).New("fake", "https://r", port.RouterOptions{})
	ctx := context.Background()
	_ = gw.Login(ctx, domain.Credentials{Username: "u", Password: "p"})
	inner.expireNext.Store(true)
	if _, err := gw.Devices(ctx); err != nil {
		t.Fatalf("Devices after session end: %v", err)
	}
	if n := inner.logins.Load(); n != 2 {
		t.Errorf("logins = %d, want 2 (initial + one re-login)", n)
	}
	if _, ok := gw.(port.WiFiAccessControl); !ok {
		t.Fatal("shared gateway must expose optional capabilities")
	}
	if _, err := gw.(port.WiFiAccessControl).WiFiBlockedMACs(ctx); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("missing capability: %v, want ErrNotImplemented", err)
	}
}

func TestShared_WaitHonoursContext(t *testing.T) {
	inner := &countingGateway{}
	gw, _ := sharedWith(inner).New("fake", "https://r", port.RouterOptions{})
	sg := gw.(*SharedGateway)
	sg.sem <- struct{}{} // someone holds the session
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := gw.Devices(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded while waiting", err)
	}
}

type serialGateway struct {
	countingGateway
	serial string
}

func (g *serialGateway) SerialNumber(context.Context) (string, error) { return g.serial, nil }

// TestShared_SerialNumber: the shared gateway forwards the optional
// BoxIdentifier capability, and answers ErrNotImplemented for an adapter that
// cannot read its serial (the model is not supported for Pro).
func TestShared_SerialNumber(t *testing.T) {
	ctx := context.Background()
	gw, err := sharedWith(&serialGateway{serial: "ZTEG00000001"}).New("fake", "http://box", port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	id, ok := gw.(port.BoxIdentifier)
	if !ok {
		t.Fatal("shared gateway does not expose port.BoxIdentifier")
	}
	if got, err := id.SerialNumber(ctx); err != nil || got != "ZTEG00000001" {
		t.Errorf("SerialNumber = (%q, %v), want ZTEG00000001", got, err)
	}

	plain, err := sharedWith(&countingGateway{}).New("fake", "http://box", port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.(port.BoxIdentifier).SerialNumber(ctx); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("SerialNumber on an adapter without it: err = %v, want ErrNotImplemented", err)
	}
}

type changerGateway struct {
	countingGateway
	got domain.Credentials
	new string
}

func (g *changerGateway) ChangeAdminPassword(_ context.Context, cur domain.Credentials, pw string) error {
	g.got, g.new = cur, pw
	return nil
}

// The first-password change (#92) goes through the shared session, which then
// counts as logged in with the new password: the next call does not log in.
func TestShared_ChangeAdminPassword(t *testing.T) {
	ctx := context.Background()
	inner := &changerGateway{}
	gw, _ := sharedWith(inner).New("fake", "https://r", port.RouterOptions{})
	ch, ok := gw.(port.AdminPasswordChanger)
	if !ok {
		t.Fatal("shared gateway does not expose port.AdminPasswordChanger")
	}
	if err := ch.ChangeAdminPassword(ctx, domain.Credentials{Username: "user", Password: "Label123"}, "Kergui2026"); err != nil {
		t.Fatal(err)
	}
	if inner.got.Password != "Label123" || inner.new != "Kergui2026" {
		t.Fatalf("inner got %v -> %q", inner.got, inner.new)
	}
	if err := gw.Login(ctx, domain.Credentials{Username: "user", Password: "Kergui2026"}); err != nil {
		t.Fatal(err)
	}
	if n := inner.logins.Load(); n != 0 {
		t.Errorf("logged in %d more time(s) with the new password, want 0", n)
	}

	plain, _ := sharedWith(&countingGateway{}).New("fake", "https://r", port.RouterOptions{})
	if err := plain.(port.AdminPasswordChanger).ChangeAdminPassword(ctx, domain.Credentials{}, "Kergui2026"); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("adapter without it: %v, want ErrNotImplemented", err)
	}
}

type lockGateway struct {
	countingGateway
	box     domain.WiFiLockBox
	applied domain.WiFiLockTarget
	aps     []string
	mode    string
}

func (g *lockGateway) WiFiLockBox(context.Context) (domain.WiFiLockBox, error) { return g.box, nil }
func (g *lockGateway) ApplyWiFiLock(_ context.Context, t domain.WiFiLockTarget) error {
	g.applied = t
	return nil
}
func (g *lockGateway) SetWiFiMode(_ context.Context, aps []string, mode string) error {
	g.aps, g.mode = aps, mode
	return nil
}

// The closed Wi-Fi's capability (RFC-007 D7) goes through the shared session,
// and an adapter without it answers ErrNotImplemented.
func TestShared_WiFiAllowList(t *testing.T) {
	ctx := context.Background()
	inner := &lockGateway{box: domain.WiFiLockBox{Limit: 64}}
	gw, _ := sharedWith(inner).New("fake", "https://r", port.RouterOptions{})
	al, ok := gw.(port.WiFiAllowList)
	if !ok {
		t.Fatal("shared gateway does not expose port.WiFiAllowList")
	}
	if b, err := al.WiFiLockBox(ctx); err != nil || b.Limit != 64 {
		t.Errorf("WiFiLockBox = %+v, %v", b, err)
	}
	target := domain.WiFiLockTarget{APs: []string{"AP1"}, Mode: domain.WiFiModeAllow, MACs: []domain.MAC{domain.MustMAC("aa:bb:cc:00:00:01")}}
	if err := al.ApplyWiFiLock(ctx, target); err != nil || inner.applied.Mode != domain.WiFiModeAllow {
		t.Errorf("ApplyWiFiLock: %v, inner got %+v", err, inner.applied)
	}
	if err := al.SetWiFiMode(ctx, []string{"AP1"}, domain.WiFiModeDisabled); err != nil || inner.mode != domain.WiFiModeDisabled {
		t.Errorf("SetWiFiMode: %v, inner got %v %q", err, inner.aps, inner.mode)
	}

	plain, _ := sharedWith(&countingGateway{}).New("fake", "https://r", port.RouterOptions{})
	pl := plain.(port.WiFiAllowList)
	if _, err := pl.WiFiLockBox(ctx); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("WiFiLockBox without it: %v, want ErrNotImplemented", err)
	}
	if err := pl.ApplyWiFiLock(ctx, target); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("ApplyWiFiLock without it: %v, want ErrNotImplemented", err)
	}
	if err := pl.SetWiFiMode(ctx, []string{"AP1"}, domain.WiFiModeAllow); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("SetWiFiMode without it: %v, want ErrNotImplemented", err)
	}
}
