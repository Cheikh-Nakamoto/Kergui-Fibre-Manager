package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/backup"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Issue #90: the PC relay sends the household's Kergui backup (the
// `kergui export` archive) to the hub with its snapshots, at most once a
// day, so the PC's names reach the household's phones.

const (
	bkBoxID    = "192-168-1-1"
	bkPassword = "motdepasse-de-la-box"
)

// backupHub is the fake hub plus the backup route, answering with the
// status the test chooses.
type backupHub struct {
	inner *fakeHub

	mu        sync.Mutex
	status    []int // answers to the next PUTs, 200 once they run out
	puts      []backupPut
	snapshots int
}

type backupPut struct {
	path, auth string
	archive    []byte
}

func (h *backupHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/relay/v1/backups/"):
		var body struct {
			Archive string `json:"archive"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		archive, _ := base64.StdEncoding.DecodeString(body.Archive)
		h.mu.Lock()
		h.puts = append(h.puts, backupPut{path: r.URL.Path, auth: r.Header.Get("Authorization"), archive: archive})
		code := http.StatusOK
		if len(h.status) > 0 {
			code, h.status = h.status[0], h.status[1:]
		}
		h.mu.Unlock()
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"boxId":"` + bkBoxID + `"}`))
	case r.Method == http.MethodPost && r.URL.Path == "/relay/v1/snapshots":
		h.mu.Lock()
		h.snapshots++
		h.mu.Unlock()
		h.inner.ServeHTTP(w, r)
	default:
		h.inner.ServeHTTP(w, r)
	}
}

func (h *backupHub) answer(codes ...int) {
	h.mu.Lock()
	h.status = append(h.status, codes...)
	h.mu.Unlock()
}

func (h *backupHub) sent() []backupPut {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]backupPut(nil), h.puts...)
}

func (h *backupHub) snapshotCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snapshots
}

// pcExport is the PC's `kergui export`, with what must never leave it
// slipped into the archive.
type pcExport struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (e *pcExport) export(context.Context) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.err != nil {
		return nil, e.err
	}
	archive, err := backup.Archive{
		Devices: []backup.Device{{MAC: houseKid, Name: "Tablette d'Awa", Notes: "chambre", FirstSeen: arT0.Unix()}},
		Usage:   []backup.Usage{{MAC: houseKid, Month: "2026-11", Bytes: 1234}},
	}.Bytes()
	if err != nil {
		return nil, err
	}
	// A file beside the two CSVs carrying the box password and the vault:
	// it must not reach the hub.
	r, _ := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range r.File {
		rc, _ := f.Open()
		w, _ := zw.Create(f.Name)
		_, _ = io.Copy(w, rc)
		_ = rc.Close()
	}
	w, _ := zw.Create("coffre.json")
	_, _ = w.Write([]byte(`{"password":"` + bkPassword + `","masterKey":"k"}`))
	_ = zw.Close()
	return buf.Bytes(), nil
}

type backupWorld struct {
	hub    *backupHub
	export *pcExport
	clock  *arClock
	agent  *Agent
}

func newBackupWorld(t *testing.T, withBackup bool) *backupWorld {
	t.Helper()
	owner, _ := trust.Generate(rand.Reader)
	relay, _ := trust.Generate(rand.Reader)
	s, err := trust.Sign(owner, trust.DomainBundle, trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: owner.Public(),
		Relays: []trust.Relay{{AgentID: arAgent, Kind: "pc", Keys: relay.Public()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := &backupWorld{hub: &backupHub{inner: &fakeHub{bundle: s}}, export: &pcExport{}, clock: &arClock{t: arT0}}
	srv := httptest.NewServer(w.hub)
	t.Cleanup(srv.Close)
	cfg := Config{
		HubURL: srv.URL, Handler: newHouseBox(),
		State: &MemoryState{Data: StateData{
			HubURL: srv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: owner.Public().SigningKey(),
		}},
		LANIP:   arLANIP,
		Clock:   w.clock.now,
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Minute, HubTimeout: 5 * time.Second},
	}
	if withBackup {
		cfg.Backup = w.export.export
		cfg.BoxID = bkBoxID
	}
	w.agent, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// at runs one round of the relay at T0 + d.
func (w *backupWorld) at(d time.Duration) {
	w.clock.set(arT0.Add(d))
	w.agent.tick(context.Background())
}

func TestBackup_RelaySendsTheExportAtMostOnceADay(t *testing.T) {
	w := newBackupWorld(t, true)
	w.at(0)

	puts := w.hub.sent()
	if len(puts) != 1 {
		t.Fatalf("backups sent on the first round = %d, want 1", len(puts))
	}
	p := puts[0]
	if p.path != "/relay/v1/backups/"+bkBoxID {
		t.Errorf("PUT %s, want /relay/v1/backups/%s", p.path, bkBoxID)
	}
	if p.auth != "Bearer kga_test" {
		t.Errorf("Authorization = %q, want the relay token", p.auth)
	}
	got, err := backup.Read(p.archive)
	if err != nil {
		t.Fatalf("the hub received no Kergui backup: %v", err)
	}
	if len(got.Devices) != 1 || got.Devices[0].Name != "Tablette d'Awa" || len(got.Usage) != 1 {
		t.Errorf("archive content = %+v", got)
	}
	// Privacy (#70): the two CSVs and nothing else, never the password.
	zr, err := zip.NewReader(bytes.NewReader(p.archive), int64(len(p.archive)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != backup.DevicesFile+","+backup.UsageFile {
		t.Errorf("archive entries = %v, want only %s and %s", names, backup.DevicesFile, backup.UsageFile)
	}
	if bytes.Contains(p.archive, []byte(bkPassword)) || bytes.Contains(p.archive, []byte("masterKey")) {
		t.Error("the box password or the vault reached the hub")
	}

	// Snapshots keep their pace; the backup waits a day.
	for _, d := range []time.Duration{time.Minute, time.Hour, 23*time.Hour + 59*time.Minute} {
		w.at(d)
	}
	if n := len(w.hub.sent()); n != 1 {
		t.Fatalf("backups sent within the day = %d, want 1", n)
	}
	if w.hub.snapshotCount() < 4 {
		t.Errorf("snapshots = %d, the backup must not hold them back", w.hub.snapshotCount())
	}
	w.at(24 * time.Hour)
	if n := len(w.hub.sent()); n != 2 {
		t.Fatalf("backups sent after a day = %d, want 2", n)
	}
}

func TestBackup_AFailureIsRetriedOnTheNextCycleWithoutLooping(t *testing.T) {
	for _, code := range []int{http.StatusInternalServerError, http.StatusUnauthorized} {
		w := newBackupWorld(t, true)
		w.hub.answer(code)
		w.at(0)
		if n := len(w.hub.sent()); n != 1 {
			t.Fatalf("%d: tries = %d, want 1", code, n)
		}
		if w.hub.snapshotCount() == 0 {
			t.Fatalf("%d: a failed backup held the snapshots back", code)
		}
		// Rounds come back to back while the relay holds the lease: no new
		// try before the next snapshot cycle.
		w.at(time.Second)
		w.at(30 * time.Second)
		if n := len(w.hub.sent()); n != 1 {
			t.Fatalf("%d: tries before the next cycle = %d, want 1", code, n)
		}
		w.at(time.Minute)
		if n := len(w.hub.sent()); n != 2 {
			t.Fatalf("%d: tries on the next cycle = %d, want 2", code, n)
		}
		// That one went through: the next is a day later.
		w.at(2 * time.Minute)
		w.at(time.Hour)
		if n := len(w.hub.sent()); n != 2 {
			t.Fatalf("%d: tries after a success = %d, want 2", code, n)
		}
	}
}

func TestBackup_AFailingExportIsRetriedOnTheNextCycle(t *testing.T) {
	w := newBackupWorld(t, true)
	w.at(0) // sent
	// A day later the export fails: nothing to send, nothing breaks, and no
	// new try on every round.
	w.export.mu.Lock()
	w.export.err = errors.New("database locked")
	w.export.mu.Unlock()
	day := 24 * time.Hour
	w.at(day)
	w.at(day + time.Second)
	if n := len(w.hub.sent()); n != 1 {
		t.Fatalf("sent with a failing export = %d, want 1", n)
	}
	if w.hub.snapshotCount() < 2 {
		t.Fatal("a failing export held the snapshots back")
	}
	w.export.mu.Lock()
	calls := w.export.calls
	w.export.err = nil
	w.export.mu.Unlock()
	if calls != 2 {
		t.Fatalf("export calls = %d, want 2 (once at T0, once on the failing cycle, not every round)", calls)
	}
	w.at(day + time.Minute)
	if n := len(w.hub.sent()); n != 2 {
		t.Fatalf("sent once the export works again = %d, want 2", n)
	}
}

func TestBackup_TooLargeIsNotRetriedInALoop(t *testing.T) {
	for _, code := range []int{http.StatusRequestEntityTooLarge, http.StatusBadRequest} {
		w := newBackupWorld(t, true)
		w.hub.answer(code)
		w.at(0)
		for _, d := range []time.Duration{time.Minute, 10 * time.Minute, time.Hour, 23 * time.Hour} {
			w.at(d)
		}
		if n := len(w.hub.sent()); n != 1 {
			t.Fatalf("%d: tries within the day = %d, want 1 (the same archive would be refused again)", code, n)
		}
		w.at(24 * time.Hour)
		if n := len(w.hub.sent()); n != 2 {
			t.Fatalf("%d: tries the next day = %d, want 2", code, n)
		}
	}
}

func TestBackup_NothingSentWithoutAnExport(t *testing.T) {
	w := newBackupWorld(t, false)
	w.at(0)
	w.at(25 * time.Hour)
	if n := len(w.hub.sent()); n != 0 {
		t.Fatalf("backups sent by a relay with no export = %d, want 0", n)
	}
}
