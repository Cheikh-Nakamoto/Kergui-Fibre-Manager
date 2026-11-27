package complaints

import "testing"

// The Play app names no payment (RFC-006, #118): its « Mon compte » sends
// « compte », filed under the team's payment category; the web's own
// « paiement » and the other categories pass as they are.
func TestNormalizeCategory(t *testing.T) {
	for in, want := range map[string]string{
		"compte":      CategoryPayment,
		" Compte ":    CategoryPayment,
		"paiement":    CategoryPayment,
		"box":         CategoryBox,
		"application": CategoryApp,
		"autre":       CategoryOther,
		"réseau":      "réseau",
	} {
		if got := NormalizeCategory(in); got != want {
			t.Errorf("NormalizeCategory(%q) = %q, want %q", in, got, want)
		}
	}
	if !ValidCategory(NormalizeCategory("compte")) {
		t.Error("the app's « compte » is refused")
	}
	if ValidCategory(NormalizeCategory("réseau")) {
		t.Error("an unknown category is accepted")
	}
}
