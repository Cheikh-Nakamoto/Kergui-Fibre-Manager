package domain

import (
	"errors"
	"testing"
)

// The F6600P's own rule (checkPassStrong in its forced-change page): at least
// 8 characters, a lower-case letter, an upper-case letter and a digit; ASCII.
func TestValidateBoxPassword(t *testing.T) {
	for _, ok := range []string{"Kergui2026", "aB3aB3aB", "Mot-de-passe 9", "Zz9!Zz9!Zz9!"} {
		if err := ValidateBoxPassword(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "aB3", "aB3aB3a", "kergui2026", "KERGUI2026", "KerguiKergui", "Kergui2026é", "Kergui\t2026"} {
		err := ValidateBoxPassword(bad)
		if !errors.Is(err, ErrWeakPassword) || !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%q: err = %v, want ErrWeakPassword (an ErrInvalidInput)", bad, err)
		}
	}
}
