package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// changerGateway is a box demanding a new password (#92).
type changerGateway struct {
	*fakeGateway
	calls   int
	current domain.Credentials
	next    string
	err     error
}

func (g *changerGateway) ChangeAdminPassword(_ context.Context, cur domain.Credentials, pw string) error {
	g.calls++
	g.current, g.next = cur, pw
	return g.err
}

func firstPasswordUC(rp *changerGateway) (*usecase.Authenticate, *fakeVault, *fakeRouterRepo) {
	v, r := newVault(), newRouterRepo()
	uc := usecase.NewAuthenticate(fakeFactory{gw: rp.fakeGateway, rp: rp}, v, r,
		fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f6600p", Vendor: "ZTE", Model: "F6600P"}}, fakeClock{t: time.Unix(1000, 0)}, nopLogger{})
	return uc, v, r
}

// The label password is never stored: the login that meets the demand
// answers ErrPasswordChangeRequired and persists nothing.
func TestAuthenticate_PasswordChangeRequiredStoresNothing(t *testing.T) {
	gw := &changerGateway{fakeGateway: &fakeGateway{meta: f660Meta(), loginErr: domain.ErrPasswordChangeRequired}}
	uc, v, r := firstPasswordUC(gw)
	_, err := uc.Execute(context.Background(), usecase.AuthenticateInput{
		BaseURL: "https://192.168.1.1", Creds: domain.Credentials{Username: "user", Password: "Label123"}, Persist: true,
	})
	if !errors.Is(err, domain.ErrPasswordChangeRequired) {
		t.Fatalf("err = %v", err)
	}
	if len(v.stored) != 0 || len(r.saved) != 0 {
		t.Fatal("the label password or the router was stored")
	}
}

func TestFirstPassword_StoresTheNewPassword(t *testing.T) {
	gw := &changerGateway{fakeGateway: &fakeGateway{meta: f660Meta()}}
	uc, v, r := firstPasswordUC(gw)
	res, err := uc.FirstPassword(context.Background(), usecase.FirstPasswordInput{
		BaseURL: "https://192.168.1.1", Creds: domain.Credentials{Username: "user", Password: "Label123"}, NewPassword: "Kergui2026",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gw.calls != 1 || gw.current.Password != "Label123" || gw.next != "Kergui2026" {
		t.Fatalf("gateway got %d call(s): %v -> %q", gw.calls, gw.current, gw.next)
	}
	if got := v.stored[res.RouterID]; got.Username != "user" || got.Password != "Kergui2026" || !res.Stored {
		t.Fatalf("vault holds %v (stored=%v)", got, res.Stored)
	}
	if rt, ok := r.saved[res.RouterID]; !ok || rt.AdapterID != "zte_f6600p" || rt.Model != "F6600P" {
		t.Fatalf("router recorded = %+v (%v)", rt, ok)
	}
}

func TestFirstPassword_Refusals(t *testing.T) {
	ctx := context.Background()
	in := usecase.FirstPasswordInput{BaseURL: "https://192.168.1.1", Creds: domain.Credentials{Username: "user", Password: "Label123"}, NewPassword: "kergui"}

	gw := &changerGateway{fakeGateway: &fakeGateway{meta: f660Meta()}}
	uc, v, _ := firstPasswordUC(gw)
	if _, err := uc.FirstPassword(ctx, in); !errors.Is(err, domain.ErrWeakPassword) || gw.calls != 0 {
		t.Fatalf("weak: %v, %d call(s)", err, gw.calls)
	}

	gw.err = domain.ErrPasswordRejected
	in.NewPassword = "Kergui2026"
	if _, err := uc.FirstPassword(ctx, in); !errors.Is(err, domain.ErrPasswordRejected) || len(v.stored) != 0 {
		t.Fatalf("refused by the box: %v, stored %v", err, v.stored)
	}

	plain := usecase.NewAuthenticate(fakeFactory{gw: &fakeGateway{meta: f660Meta()}}, newVault(), newRouterRepo(),
		fakeDisco{info: domain.RouterInfo{AdapterID: "zte_f660"}}, fakeClock{}, nopLogger{})
	if _, err := plain.FirstPassword(ctx, in); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("adapter without it: %v", err)
	}
}
