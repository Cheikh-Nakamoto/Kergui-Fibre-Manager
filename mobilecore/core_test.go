package mobilecore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *captureLogger) Log(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, level+": "+msg)
}

func newCore(t *testing.T, log Logger) *Core {
	t.Helper()
	c, err := Open(t.TempDir(), "test-vault-key", log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func call(t *testing.T, c *Core, method, path, body string) (int, string) {
	t.Helper()
	res, err := c.Request(method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res.Status, res.Body
}

type device struct {
	MAC       string
	Blocked   bool
	NewlySeen bool
}

func devices(t *testing.T, c *Core) map[string]device {
	t.Helper()
	status, body := call(t, c, "GET", "/api/devices", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/devices = %d %s", status, body)
	}
	var list []device
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode devices: %v\n%s", err, body)
	}
	out := map[string]device{}
	for _, d := range list {
		out[strings.ToLower(d.MAC)] = d
	}
	return out
}

func TestOpenRejectsEmptyVaultKey(t *testing.T) {
	if _, err := Open(t.TempDir(), " ", nil); err == nil {
		t.Fatal("want an error for an empty vault key")
	}
}

func TestRequestBeforeConfigure(t *testing.T) {
	c := newCore(t, nil)
	if _, err := c.Request("GET", "/api/health", ""); err == nil {
		t.Fatal("want an error before Configure")
	}
}

func TestConfigureRejectsBadURL(t *testing.T) {
	c := newCore(t, nil)
	for _, u := range []string{"", "192.168.1.1", "ftp://192.168.1.1", "http://"} {
		if err := c.Configure(u, "", ""); err == nil {
			t.Errorf("Configure(%q) accepted", u)
		}
	}
}

// TestDemoFlow walks the path a store reviewer takes: demo mode, login, list,
// block then unblock (each write reread from the box), rename.
func TestDemoFlow(t *testing.T) {
	log := &captureLogger{}
	c := newCore(t, log)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if demoURL := c.DemoURL(); !strings.HasPrefix(demoURL, "http://127.0.0.1:") {
		t.Fatalf("demo URL = %q", demoURL)
	}

	if status, body := call(t, c, "POST", "/api/login", `{"username":"admin","password":"wrong","test":true}`); status != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password = %d %s, want 401", status, body)
	}
	status, body := call(t, c, "POST", "/api/login", `{"username":"`+DemoUsername+`","password":"`+DemoPassword+`"}`)
	if status != http.StatusOK || !strings.Contains(body, `"stored": true`) {
		t.Fatalf("login = %d %s", status, body)
	}
	if strings.Contains(body, DemoPassword+`"`) {
		t.Fatalf("login response echoes the password: %s", body)
	}

	const mac = "ac:bb:cc:00:00:22" // PC-Bureau in the fixture, not blocked
	first := devices(t, c)
	if len(first) == 0 {
		t.Fatal("no devices in demo mode")
	}
	if d, ok := first[mac]; !ok || d.Blocked || !d.NewlySeen {
		t.Fatalf("first listing of %s = %+v (present %v)", mac, d, ok)
	}
	if d := devices(t, c)[mac]; d.NewlySeen {
		t.Fatalf("%s still flagged new on the second listing", mac)
	}

	if status, body := call(t, c, "POST", "/api/devices/"+mac+"/block", ""); status != http.StatusOK {
		t.Fatalf("block = %d %s", status, body)
	}
	if !devices(t, c)[mac].Blocked {
		t.Fatal("device not blocked after block")
	}
	if status, body := call(t, c, "POST", "/api/devices/"+mac+"/unblock", ""); status != http.StatusOK {
		t.Fatalf("unblock = %d %s", status, body)
	}
	if devices(t, c)[mac].Blocked {
		t.Fatal("device still blocked after unblock")
	}

	if status, body := call(t, c, "PATCH", "/api/devices/"+mac, `{"custom_name":"Bureau"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}
	if status, _ := call(t, c, "POST", "/api/devices/not-a-mac/block", ""); status != http.StatusBadRequest {
		t.Fatalf("block with a bad MAC = %d, want 400", status)
	}

	log.mu.Lock()
	defer log.mu.Unlock()
	for _, l := range log.lines {
		if strings.Contains(l, `"password"`) || strings.Contains(l, "password=") {
			t.Fatalf("a log line carries the password: %q", l)
		}
	}
}

func TestOnlyV1RoutesAreReachable(t *testing.T) {
	c := newCore(t, nil)
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct{ method, path string }{
		{"GET", "/"},
		{"GET", "/api/members"},
		{"GET", "/api/invoices"},
		{"POST", "/api/billing/close"},
		{"POST", "/paiement/webhook"},
		{"GET", "/api/config/env"},
	} {
		if status, _ := call(t, c, r.method, r.path, ""); status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 404/405", r.method, r.path, status)
		}
	}
	if status, body := call(t, c, "GET", "/api/health", ""); status != http.StatusOK {
		t.Fatalf("health = %d %s", status, body)
	}
}

func TestCertificatePinning(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><title>box</title></html>"))
	}))
	defer srv.Close()

	pin, err := CertificateFingerprint(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(pin) != 64 {
		t.Fatalf("fingerprint = %q, want 64 hex chars", pin)
	}
	if _, err := CertificateFingerprint("http://192.168.1.1"); err == nil {
		t.Fatal("want an error for a plain-HTTP URL")
	}

	c := newCore(t, nil)
	if err := c.Configure(srv.URL, "", strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	// Discovery is the first call the app makes: with the wrong pin it must
	// fail as a 502 that names the pin, so the app can warn about it.
	status, body := call(t, c, "GET", "/api/discover", "")
	if status != http.StatusBadGateway || !strings.Contains(body, "pinned fingerprint") {
		t.Fatalf("discover with a wrong pin = %d %s, want 502 naming the pin", status, body)
	}

	if err := c.Configure(srv.URL, "", pin); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "GET", "/api/discover", ""); status != http.StatusOK {
		t.Fatalf("discover with the right pin = %d %s", status, body)
	}
}

// TestJournalRecordsWhatTheAppNeeds: the journal screen is what a household
// can show someone when the box does not answer, so it has to hold both the
// core's own calls and what only the app knows — and it has to work before
// the core is pointed at anything.
func TestJournalRecordsWhatTheAppNeeds(t *testing.T) {
	c := newCore(t, nil)

	// Before Configure: the connection screen's own failures are exactly
	// what matters here.
	c.Note("warn", "adresse saisie: 192.168.1.1")
	first := decodeJournal(t, c, 0, 0)
	if len(first) == 0 {
		t.Fatal("the journal is empty before the core is configured")
	}
	last := first[len(first)-1]
	// The level is the logger's own spelling ("WARN"), as the dashboard
	// already receives it; the app lowercases it for display.
	if !strings.EqualFold(last.Level, "warn") || !strings.Contains(last.Msg, "192.168.1.1") {
		t.Errorf("last entry = %+v, want the app's own note", last)
	}

	// After a call: one line per API call, with its status.
	if err := c.StartDemo(); err != nil {
		t.Fatal(err)
	}
	if status, body := call(t, c, "GET", "/api/health", ""); status != http.StatusOK {
		t.Fatalf("health = %d %s", status, body)
	}
	entries := decodeJournal(t, c, last.Seq, 0)
	var sawCall bool
	for _, e := range entries {
		if strings.Contains(e.Msg, "HTTP GET /api/health -> 200") {
			sawCall = true
		}
		if e.Seq <= last.Seq {
			t.Errorf("since was not honoured: entry %d", e.Seq)
		}
	}
	if !sawCall {
		t.Errorf("no entry records the API call: %+v", entries)
	}

	// The journal's own polling is not in the journal, or it would grow by
	// reading it.
	if status, _ := call(t, c, "GET", "/api/logs", ""); status != http.StatusOK {
		t.Fatalf("GET /api/logs = %d, want 200: the app's journal screen reads it", status)
	}
	for _, e := range decodeJournal(t, c, 0, 0) {
		if strings.Contains(e.Msg, "/api/logs") {
			t.Errorf("reading the journal wrote to it: %q", e.Msg)
		}
	}

	// A note is capped rather than allowed to fill the ring.
	c.Note("info", strings.Repeat("x", 4000))
	for _, e := range decodeJournal(t, c, 0, 0) {
		if len(e.Msg) > 600 {
			t.Errorf("an app note of %d characters was kept whole", len(e.Msg))
		}
	}
}

// TestJournalLimit: the app asks for a page, not the whole ring.
func TestJournalLimit(t *testing.T) {
	c := newCore(t, nil)
	for i := 0; i < 10; i++ {
		c.Note("info", fmt.Sprintf("ligne %d", i))
	}
	entries := decodeJournal(t, c, 0, 3)
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want the 3 asked for", len(entries))
	}
	if !strings.Contains(entries[2].Msg, "ligne 9") {
		t.Errorf("the newest entry is %q, want the last line", entries[2].Msg)
	}
}

func decodeJournal(t *testing.T, c *Core, since int64, limit int) []port.LogEntry {
	t.Helper()
	raw, err := c.Journal(since, limit)
	if err != nil {
		t.Fatal(err)
	}
	var out []port.LogEntry
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("the journal is not readable JSON: %v", err)
	}
	return out
}
