package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// A new box demanding a new password (#92): the PC sends the household to
// the app, in French, and offers no flow of its own.
func TestFriendly_PasswordChangeRequired(t *testing.T) {
	msg := friendly(fmt.Errorf("login: %w", domain.ErrPasswordChangeRequired))
	for _, want := range []string{"nouveau mot de passe", "application Kergui"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}
