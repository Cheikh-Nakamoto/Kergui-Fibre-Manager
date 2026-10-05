package zte_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/zte"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef660"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

func newGateway(t *testing.T) (*zte.Gateway, func()) {
	t.Helper()
	srv := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	gw, err := zte.New(ztef660.Profile(), srv.URL, port.RouterOptions{})
	if err != nil {
		srv.Close()
		t.Fatalf("new gateway: %v", err)
	}
	return gw, srv.Close
}

func TestGateway_LoginSuccessAndReads(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()

	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatalf("login: %v", err)
	}

	info, err := gw.RouterInfo(ctx)
	if err != nil || info.Model != "ZXHN F660" || info.Firmware != "V6.0.10P2T2" {
		t.Fatalf("router info = (%+v, %v)", info, err)
	}

	devs, err := gw.Devices(ctx)
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(devs) != 4 {
		t.Fatalf("want 4 devices, got %d", len(devs))
	}
	blocked := map[string]bool{}
	for _, d := range devs {
		blocked[d.MAC.String()] = d.Blocked
	}
	if !blocked["ac:bb:cc:00:00:33"] {
		t.Error("TV (ac:bb:cc:00:00:33) should be flagged blocked from the ACL")
	}
	if blocked["ac:bb:cc:00:00:11"] {
		t.Error("phone should not be blocked")
	}

	rules, err := gw.AccessRules(ctx)
	if err != nil || len(rules) != 2 {
		t.Fatalf("access rules = (%d, %v)", len(rules), err)
	}
}

func TestGateway_LoginFailure(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()
	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "wrong"}); !errors.Is(err, domain.ErrAuthFailed) {
		t.Fatalf("login err = %v, want ErrAuthFailed", err)
	}
}

func TestGateway_ReadWithoutSession(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()
	// No login performed → the mock rejects the page with 401.
	if _, err := gw.AccessRules(ctx); !errors.Is(err, domain.ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
}

func TestGateway_WriteOpsNotImplemented(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	if err := gw.Block(ctx, mac); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("Block err = %v, want ErrNotImplemented", err)
	}
	if err := gw.Unblock(ctx, mac); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("Unblock err = %v, want ErrNotImplemented", err)
	}
}
