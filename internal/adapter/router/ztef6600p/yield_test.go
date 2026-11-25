package ztef6600p

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Issue #94: the box keeps one admin session. The owner's app evicts whoever
// holds it; a relay that is told to yield leaves the owner's session alone.

const ownerAtHome = "192.168.1.17(user)"

func parkingBox(t *testing.T) (*mockrouter.F6600P, string) {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "admin")
	box.EnableParking()
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	return box, srv.URL
}

func TestGateway_YieldsToAnotherSessionWhenTold(t *testing.T) {
	box, url := parkingBox(t)
	box.HoldSession(ownerAtHome)

	var asked []string
	gw, err := New(url, port.RouterOptions{Preempt: func(holder string) bool {
		asked = append(asked, holder)
		return false
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = gw.Login(context.Background(), domain.Credentials{Username: "admin", Password: "admin"})
	if !errors.Is(err, domain.ErrSessionHeld) {
		t.Fatalf("Login = %v, want ErrSessionHeld", err)
	}
	if errors.Is(err, domain.ErrAuthFailed) {
		t.Fatal("a held session is not a wrong password")
	}
	if len(asked) != 1 || asked[0] != ownerAtHome {
		t.Fatalf("policy asked about %v, want [%s]", asked, ownerAtHome)
	}
	if got := box.Holder(); got != ownerAtHome || box.Preempts() != 0 {
		t.Fatalf("the box is held by %q after %d preempts, want the owner's untouched session", got, box.Preempts())
	}
}

func TestGateway_OwnerAppTakesTheBox(t *testing.T) {
	box, url := parkingBox(t)
	box.HoldSession("192.168.1.5(user)") // the relay

	gw, err := New(url, port.RouterOptions{}) // no policy: the owner's app
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if box.Preempts() != 1 || box.Holder() == "192.168.1.5(user)" {
		t.Fatalf("preempts = %d, holder %q: the owner's app did not take the box", box.Preempts(), box.Holder())
	}
	if _, err := gw.RouterInfo(ctx); err != nil {
		t.Fatalf("RouterInfo after the takeover: %v", err)
	}
}

func TestGateway_ReloginYieldsMidCall(t *testing.T) {
	// The relay is logged in; the owner's phone takes the box; the relay's
	// next read finds its session gone, logs in again, and yields.
	box, url := parkingBox(t)
	yield := false
	gw, err := New(url, port.RouterOptions{Preempt: func(string) bool { return !yield }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	box.HoldSession(ownerAtHome)
	yield = true
	if _, err := gw.Devices(ctx); !errors.Is(err, domain.ErrSessionHeld) {
		t.Fatalf("Devices = %v, want ErrSessionHeld through the re-login", err)
	}
	if box.Holder() != ownerAtHome {
		t.Fatalf("holder %q, want the owner's", box.Holder())
	}
}
