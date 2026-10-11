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
