package identity

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

func TestOnlyAdminAndViewerCanBeGiven(t *testing.T) {
	for _, r := range []Role{RoleAdmin, RoleViewer} {
		if _, ok := r.kernelRole(); !ok {
			t.Errorf("%s refused", r)
		}
	}
	for _, r := range []Role{RoleOwner, "agent", "", "ROLE_ADMIN"} {
		if _, ok := r.kernelRole(); ok {
			t.Errorf("%q accepted", r)
		}
	}
	if roleOf(memberdomain.MEMBER_ROLE_AGENT) != RoleViewer {
		t.Error("the kernel's agent role must read as the narrowest one")
	}
}

func TestInviteTextNamesTheInviterTheRoleAndTheDeadline(t *testing.T) {
	text := InviteText("+221771234567", "Foyer +221••••67", RoleViewer, time.Date(2026, 11, 29, 9, 0, 0, 0, time.UTC))
	for _, want := range []string{"Kergui", "+221771234567", "Foyer +221••••67", "lecteur", "29/11/2026", "ce numéro"} {
		if !strings.Contains(text, want) {
			t.Errorf("invite %q lacks %q", text, want)
		}
	}
	if !strings.Contains(InviteText("", "F", RoleAdmin, time.Now()), "administrateur") {
		t.Error("admin invite")
	}
}

func TestMembersErrorsHaveTheirOwnCodes(t *testing.T) {
	cases := map[error]struct {
		code   string
		status int
	}{
		alerts.ErrInvalidPhone: {CodeInvalidPhone, http.StatusBadRequest},
		ErrInvalidRole:         {CodeInvalidRole, http.StatusBadRequest},
		ErrOwnerOnly:           {CodeOwnerOnly, http.StatusForbidden},
		ErrAlreadyMember:       {CodeAlreadyMember, http.StatusConflict},
		ErrOwnerUntouched:      {CodeOwnerUntouched, http.StatusBadRequest},
		ErrInviteLimit:         {CodeInviteLimit, http.StatusTooManyRequests},
		ErrMessageNotSent:      {CodeWhatsAppDown, http.StatusBadGateway},
		ErrInvalidKey:          {CodeInvalidKey, http.StatusBadRequest},
	}
	for err, want := range cases {
		var app *kernelerrors.AppError
		if !errors.As(mapErr(err), &app) || app.Code != want.code || app.HTTPCode != want.status {
			t.Errorf("%v -> %+v, want %s %d", err, app, want.code, want.status)
		}
	}
}
