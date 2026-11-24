package usecase_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// fakeIdentity stands in for the kernel bridge (internal/hub/identity): one
// household per account, a fresh token pair per sign-in.
type fakeIdentity struct {
	mu        sync.Mutex
	calls     []usecase.Account
	signedOut []usecase.Account
	err       error
}

func (f *fakeIdentity) SignOut(_ context.Context, a usecase.Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signedOut = append(f.signedOut, a)
	return nil
}

func (f *fakeIdentity) SignIn(_ context.Context, a usecase.Account) (usecase.HubSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return usecase.HubSession{}, f.err
	}
	f.calls = append(f.calls, a)
	n := string(rune('0' + len(f.calls)))
	return usecase.HubSession{
		UserID: "user-" + string(a.ID), TenantID: "tenant-" + string(a.ID),
		AccessToken: "jwt-" + n, RefreshToken: "refresh-" + n,
	}, nil
}

func TestVerifyOTP_SignsInToTheHubHouseholdToo(t *testing.T) {
	f := newFixture(t)
	id := &fakeIdentity{}
	f.svc.Identity = id
	ctx := context.Background()
	if _, err := f.svc.RequestOTP(ctx, phone); err != nil {
		t.Fatal(err)
	}
	in, err := f.svc.VerifyOTP(ctx, phone, f.sender.code(t), "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if in.Hub == nil || in.Hub.TenantID != "tenant-"+string(in.Account.ID) || in.Hub.AccessToken == "" || in.Hub.RefreshToken == "" {
		t.Fatalf("hub session %+v", in.Hub)
	}
	if len(id.calls) != 1 || id.calls[0].ID != in.Account.ID || id.calls[0].Phone != phone {
		t.Fatalf("identity called with %+v", id.calls)
	}
	if _, err := f.svc.Authenticate(ctx, in.Token); err != nil {
		t.Fatalf("the licensing session must work as well: %v", err)
	}
}

func TestVerifyOTP_WithoutIdentityHasNoHubSession(t *testing.T) {
	f := newFixture(t)
	_, _ = f.svc.RequestOTP(context.Background(), phone)
	in, err := f.svc.VerifyOTP(context.Background(), phone, f.sender.code(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if in.Hub != nil {
		t.Fatalf("hub session %+v on a hub with no identity bridge", in.Hub)
	}
}

func TestVerifyOTP_HubFailureSignsNothingIn(t *testing.T) {
	f := newFixture(t)
	boom := errors.New("kernel down")
	f.svc.Identity = &fakeIdentity{err: boom}
	_, _ = f.svc.RequestOTP(context.Background(), phone)
	if _, err := f.svc.VerifyOTP(context.Background(), phone, f.sender.code(t), ""); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	// One sign-in: no licensing session was left behind without its
	// household, so nothing is half signed in.
	if n := f.store.SessionCount(); n != 0 {
		t.Fatalf("%d sessions created", n)
	}
}

func TestHubSession_ReissuedFromTheLicensingSession(t *testing.T) {
	f := newFixture(t)
	id := &fakeIdentity{}
	f.svc.Identity = id
	acc := f.signIn(t)
	out, err := f.svc.HubSession(context.Background(), acc)
	if err != nil {
		t.Fatal(err)
	}
	if out.TenantID != "tenant-"+string(acc.ID) || out.RefreshToken != "refresh-2" {
		t.Fatalf("hub session %+v", out)
	}

	f.svc.Identity = nil
	if _, err := f.svc.HubSession(context.Background(), acc); !errors.Is(err, usecase.ErrHubIdentityOff) {
		t.Fatalf("without a bridge: %v", err)
	}
}
