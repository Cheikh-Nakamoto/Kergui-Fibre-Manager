package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// eraser records the accounts the hub's eraser was asked to delete.
type eraser struct {
	got []usecase.Account
	err error
}

func (e *eraser) EraseAccount(_ context.Context, a usecase.Account) error {
	e.got = append(e.got, a)
	return e.err
}

// TestDeleteAccount_WithoutTheHubEraserDropsTheLicensingAccount is the
// licensing-only hub (no identity bridge): the account, its sessions, its
// rights, its trial and its payments go; the session no longer opens.
func TestDeleteAccount_WithoutTheHubEraserDropsTheLicensingAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.svc.RequestOTP(ctx, phone)
	in, err := f.svc.VerifyOTP(ctx, phone, f.sender.code(t), "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	a := in.Account
	f.trial(t, a.ID, box)

	if err := f.svc.DeleteAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, in.Token); !errors.Is(err, usecase.ErrUnauthenticated) {
		t.Fatalf("the session after the deletion: %v", err)
	}
	if _, err := f.store.AccountByID(ctx, a.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("the account after the deletion: %v", err)
	}
	if subs, _ := f.store.ListSubscriptions(ctx, a.ID); len(subs) != 0 {
		t.Fatalf("subscriptions left: %v", subs)
	}
	if used, _, _ := f.store.TrialUsed(ctx, a.Phone, ""); used {
		t.Fatal("the trial is still recorded for the number")
	}
	if list, _ := f.store.AccountPayments(ctx, a.ID, 50); len(list) != 0 {
		t.Fatalf("payments left: %v", list)
	}
	// A second deletion of a gone account is not an error worth telling.
	if err := f.svc.DeleteAccount(ctx, a); err != nil {
		t.Fatalf("deleting again: %v", err)
	}
}

// TestDeleteAccount_TheHubEraserDoesItAll: with the identity bridge, the
// eraser deletes everything in one transaction, licensing rows included.
func TestDeleteAccount_TheHubEraserDoesItAll(t *testing.T) {
	f := newFixture(t)
	e := &eraser{}
	f.svc.Eraser = e
	a := f.signIn(t)
	if err := f.svc.DeleteAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if len(e.got) != 1 || e.got[0].ID != a.ID || e.got[0].Phone != a.Phone {
		t.Fatalf("eraser got %+v, want %+v", e.got, a)
	}
	// The eraser fails: so does the deletion, nothing half done is said done.
	e.err = errors.New("boom")
	if err := f.svc.DeleteAccount(context.Background(), a); err == nil {
		t.Fatal("a failed erasure was reported done")
	}
}
