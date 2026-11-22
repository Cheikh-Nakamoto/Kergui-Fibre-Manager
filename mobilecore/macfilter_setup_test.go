package mobilecore

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

func setupFilter(t *testing.T, c *Core) string {
	t.Helper()
	status, body := call(t, c, "POST", "/api/filter/setup", "")
	if status != http.StatusOK {
		t.Fatalf("POST /api/filter/setup = %d %s", status, body)
	}
	return body
}

func filterCore(t *testing.T, on bool, mode string) (*Core, *mockrouter.F6600P) {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "s3cret")
	box.SetMACFilter(on, mode)
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	c := newCore(t, nil)
	if err := c.Configure(srv.URL, "", ""); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"s3cret"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	return c, box
}

func journalText(t *testing.T, c *Core) string {
	t.Helper()
	j, err := c.Journal(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// TestFilterSetup_EnablesBlocklistOnce: the first setup turns the filter on in
// Discard mode and says so in the journal; a second one changes nothing (#56).
func TestFilterSetup_EnablesBlocklistOnce(t *testing.T) {
	c, box := filterCore(t, false, "Discard")
	if got := setupFilter(t, c); !strings.Contains(got, `"enabled"`) {
		t.Fatalf("first setup = %s, want enabled", got)
	}
	if !box.MACFilterEnabled() || box.MACFilterMode() != "Discard" {
		t.Fatalf("box filter on=%v mode=%s, want on Discard", box.MACFilterEnabled(), box.MACFilterMode())
	}
	if n := strings.Count(journalText(t, c), "Filtrage MAC activé (liste noire)"); n != 1 {
		t.Fatalf("journal has %d setup lines, want 1", n)
	}
	if got := setupFilter(t, c); !strings.Contains(got, `"already"`) {
		t.Fatalf("second setup = %s, want already", got)
	}
	if n := strings.Count(journalText(t, c), "Filtrage MAC activé (liste noire)"); n != 1 {
		t.Errorf("journal has %d setup lines after the second run, want 1", n)
	}
}

// TestFilterSetup_LeavesPermit: an allowlist the household chose is left as it
// is, with a journal line.
func TestFilterSetup_LeavesPermit(t *testing.T) {
	c, box := filterCore(t, true, "Permit")
	if got := setupFilter(t, c); !strings.Contains(got, `"permit"`) {
		t.Fatalf("setup = %s, want permit", got)
	}
	if !box.MACFilterEnabled() || box.MACFilterMode() != "Permit" {
		t.Errorf("box filter on=%v mode=%s, want untouched Permit", box.MACFilterEnabled(), box.MACFilterMode())
	}
	if !strings.Contains(journalText(t, c), "liste blanche") {
		t.Error("no journal line about the allowlist")
	}
}

// TestDemo_FilterOffUntilSetup: the demo box starts with the filter off, so
// « Essayer en démo » shows the setup step turning it on.
func TestDemo_FilterOffUntilSetup(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.StopDemo)
	if c.demoBox.MACFilterEnabled() {
		t.Fatal("the demo box starts with the filter on")
	}
	if status, body := call(t, c, "POST", "/api/login", `{"username":"`+DemoUsername+`","password":"`+DemoPassword+`"}`); status != http.StatusOK {
		t.Fatalf("login = %d %s", status, body)
	}
	setupFilter(t, c)
	if !c.demoBox.MACFilterEnabled() {
		t.Error("demo filter still off after setup")
	}
}
