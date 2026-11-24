package handler_test

import (
	"net/http"
	"testing"
)

// TestDeleteAccount is the app's « Supprimer mon compte » (#37) on the hub's
// side: only with a session, and afterwards the session is dead and another
// number's is not.
func TestDeleteAccount(t *testing.T) {
	e := newEnv(t)
	if st, _, _ := e.do("DELETE", "/licensing/v1/account", "", nil); st != http.StatusUnauthorized {
		t.Fatalf("without a session = %d, want 401", st)
	}
	tok := e.signIn()
	other := e.signInAs("+221770000001")

	if st, body, _ := e.do("DELETE", "/licensing/v1/account", tok, nil); st != http.StatusNoContent {
		t.Fatalf("delete = %d %v", st, body)
	}
	if st, _, _ := e.do("GET", "/licensing/v1/subscriptions", tok, nil); st != http.StatusUnauthorized {
		t.Fatalf("the deleted account's session = %d, want 401", st)
	}
	if st, _, _ := e.do("DELETE", "/licensing/v1/account", tok, nil); st != http.StatusUnauthorized {
		t.Fatalf("deleting again = %d, want 401", st)
	}
	if st, _, _ := e.do("GET", "/licensing/v1/subscriptions", other, nil); st != http.StatusOK {
		t.Fatalf("another number's session = %d, want 200", st)
	}
}
