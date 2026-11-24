package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
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
	// The trial row goes with the account; only its mark stays (see
	// TestDeleteAccount_KeepsTheTrialMark).
	if _, err := f.store.AccountTrial(ctx, a.ID); !errors.Is(err, usecase.ErrNotFound) {
		t.Fatalf("the trial is still recorded for the account: %v", err)
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

// A deleted account's number and box keep the mark of their trial: the
// number signs up again but is refused a second trial, and the box would
// not start one either.
func TestDeleteAccount_KeepsTheTrialMark(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	acc := f.signIn(t)
	f.trial(t, acc.ID, box)
	if err := f.svc.DeleteAccount(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RequestTrial(ctx, string(acc.Phone)); !errors.Is(err, domain.ErrTrialPhoneUsed) {
		t.Fatalf("second trial for the number: %v, want ErrTrialPhoneUsed", err)
	}
	if _, boxUsed, err := f.store.TrialUsed(ctx, "+221770000099", box); err != nil || !boxUsed {
		t.Fatalf("box after deletion: %v %v, want used", boxUsed, err)
	}
}
