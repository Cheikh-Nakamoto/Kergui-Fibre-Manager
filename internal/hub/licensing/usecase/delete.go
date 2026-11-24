package usecase

import (
	"context"
	"errors"
	"fmt"
)

// Account deletion (#37, Google Play's account-deletion policy): the app's
// « Supprimer mon compte » ends the account and everything the hub keeps
// for it. The owner's decision is to erase it all, payments included.

// AccountEraser erases an account and every row the hub keeps for it, in
// one transaction: the identity bridge (internal/hub/identity) implements
// it, since the account's household, kernel user, complaints, owner devices
// and backups live outside this module.
type AccountEraser interface {
	EraseAccount(ctx context.Context, a Account) error
}

// DeleteAccount erases the account. Through the hub's eraser when it is
// wired; otherwise (a licensing-only hub) the store drops the account and
// what this module holds for it. An account already gone is not an error.
func (s *Service) DeleteAccount(ctx context.Context, a Account) error {
	var err error
	if s.Eraser != nil {
		err = s.Eraser.EraseAccount(ctx, a)
	} else {
		err = s.Store.DeleteAccount(ctx, a)
	}
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("licensing: delete the account: %w", err)
	}
	s.info("licensing: account deleted by its holder", "account", string(a.ID), "phone", a.Phone.Masked())
	return nil
}
