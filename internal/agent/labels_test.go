package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef6600p"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Issue #95: the names and notes the owner gave are the same whether the app
// talks to the box or goes through the relay. Two real cores — the owner's
// phone and the relay — each with its own database, in front of the same
// F6600P mock; the relay runs the owner's (or a member's) signed POST
// /api/labels, and its device list then carries them.

type labelsWorld struct {
	t      *testing.T
	owner  *trust.Identity
	member *trust.Identity
	box    *mockrouter.F6600P
	clock  *arClock
	agent  *Agent
	// ownerAPI is the owner's phone's core, relayAPI the relay's.
	ownerAPI http.Handler
	relayAPI http.Handler
}

// coreAPI is a core's in-memory API on its own database, logged in to the
// box with preempt as its session policy (nil: always take the box).
func coreAPI(t *testing.T, boxURL string, preempt func(string) bool) http.Handler {
	t.Helper()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	factory := catalog.New()
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	vlt := vault.New(sqlite.NewCredentialStore(db), "test-key")
	if err := vlt.Store(context.Background(), usecase.RouterID(boxURL), domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatal(err)
	}
	log := logging.New(io.Discard, false)
	devices := sqlite.NewDeviceRepo(db)
	return httpapi.New(httpapi.Services{
		List:   usecase.NewListDevices(factory, vlt, devices, vendor.New(nil), disco, clk, log),
		Rename: usecase.NewRenameDevice(devices),
		Labels: usecase.NewLabels(devices),
	}, httpapi.Config{BaseURL: boxURL, AdapterID: ztef6600p.ID, Opts: port.RouterOptions{Preempt: preempt}}).Handler()
}

func newLabelsWorld(t *testing.T) *labelsWorld {
	t.Helper()
	gen := func() *trust.Identity {
		id, err := trust.Generate(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	relay := gen()
	w := &labelsWorld{t: t, owner: gen(), member: gen(), box: mockrouter.NewF6600P("admin", "admin"), clock: &arClock{t: arT0}}
	w.box.EnableParking()
	boxSrv := httptest.NewServer(w.box.Handler())
	t.Cleanup(boxSrv.Close)

	b := trust.Bundle{
		TenantID: arTenant, Version: 1, IssuedAt: arT0, Owner: w.owner.Public(),
		Relays: []trust.Relay{{AgentID: arAgent, Kind: "phone", Keys: relay.Public()}},
	}
	if err := b.Delegate(w.member.Public(), trust.ScopeControl, "Awa", arT0); err != nil {
		t.Fatal(err)
	}
	s, err := trust.Sign(w.owner, trust.DomainBundle, b)
	if err != nil {
		t.Fatal(err)
	}
	hub := &fakeHub{bundle: s}
	hubSrv := httptest.NewServer(hub)
	t.Cleanup(hubSrv.Close)

	y := NewYield()
	w.relayAPI = coreAPI(t, boxSrv.URL, y.AllowPreempt)
	w.ownerAPI = coreAPI(t, boxSrv.URL, nil)
	ag, err := New(Config{
		HubURL: hubSrv.URL, Handler: w.relayAPI, Yield: y,
		State: &MemoryState{Data: StateData{
			HubURL: hubSrv.URL, AgentID: arAgent, TenantID: arTenant, Token: "kga_test",
			Identity: relay.Marshal(), OwnerKey: w.owner.Public().SigningKey(),
		}},
		LANIP: yieldLANIP,
		Clock: w.clock.now,
		Timings: Timings{Heartbeat: time.Second, LongPoll: time.Millisecond, SnapshotEvery: time.Second,
			HubTimeout: 5 * time.Second, YieldBackoff: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	w.agent = ag
	w.round()
	return w
}

func (w *labelsWorld) round() {
	w.clock.set(w.clock.now().Add(2 * time.Second))
	w.agent.tick(context.Background())
}

// call runs one request on a core's API.
func (w *labelsWorld) call(api http.Handler, method, path, body string) (int, string) {
	w.t.Helper()
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec.Code, rec.Body.String()
}

// send is the hub handing the relay a command signed by who, with its body.
func (w *labelsWorld) send(who *trust.Identity, method, path, body string) (int, string) {
	w.t.Helper()
	sc, err := trust.NewCommand(who, arTenant, method, path, []byte(body), w.clock.now(), 30*time.Second)
	if err != nil {
		w.t.Fatal(err)
	}
	cmd, _ := sc.Unverified()
	status, out := w.agent.run(context.Background(), &queuedCommand{ID: cmd.CmdID, Epoch: 1, Envelope: sc, Body: []byte(body)})
	return status, string(out)
}

type listed struct {
	MAC        string
	CustomName string
	LabelAt    time.Time
}

// devices is a core's device list, by MAC.
func (w *labelsWorld) devices(api http.Handler) map[string]listed {
	w.t.Helper()
	status, body := w.call(api, "GET", "/api/devices", "")
	if status != http.StatusOK {
		w.t.Fatalf("GET /api/devices = %d %s", status, body)
	}
	var list []listed
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		w.t.Fatal(err)
	}
	out := map[string]listed{}
	for _, d := range list {
		out[strings.ToLower(d.MAC)] = d
	}
	return out
}

// relayList is the device list as the relay signs it into its snapshot.
func (w *labelsWorld) relayList() map[string]listed {
	w.t.Helper()
	body, err := w.agent.read(context.Background(), "/api/devices")
	if err != nil {
		w.t.Fatal(err)
	}
	var list []listed
	if err := json.Unmarshal(body, &list); err != nil {
		w.t.Fatal(err)
	}
	out := map[string]listed{}
	for _, d := range list {
		out[strings.ToLower(d.MAC)] = d
	}
	return out
}

func (w *labelsWorld) anyMAC() string {
	for m := range w.devices(w.ownerAPI) {
		return m
	}
	w.t.Fatal("the box lists no device")
	return ""
}

// changedOf reads the MACs POST /api/labels says it changed.
func changedOf(body string) []string {
	var out struct {
		Changed []string `json:"changed"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out.Changed
}

func labelsBody(mac, name string, at time.Time) string {
	b, _ := json.Marshal(httpapi.LabelsJSON{Labels: []httpapi.LabelJSON{{MAC: mac, Name: &name, At: at}}})
	return string(b)
}

func TestLabels_ANewRelayReceivesTheOwnersNames(t *testing.T) {
	w := newLabelsWorld(t)
	mac := w.anyMAC()

	// Named on the owner's phone, at home, long before the relay existed.
	if status, body := w.call(w.ownerAPI, "PATCH", "/api/devices/"+mac, `{"custom_name":"Laptop de Moussa"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}
	if got := w.relayList()[mac].CustomName; got != "" {
		t.Fatalf("the new relay already shows %q", got)
	}

	// The owner's app sends its labels, signed, as it is.
	status, labels := w.call(w.ownerAPI, "GET", "/api/labels", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/labels = %d %s", status, labels)
	}
	if status, body := w.send(w.owner, "POST", RouteLabels, labels); status != http.StatusOK || !strings.Contains(body, mac) {
		t.Fatalf("labels on the relay = %d %s", status, body)
	}
	got := w.relayList()[mac]
	want := w.devices(w.ownerAPI)[mac]
	if got.CustomName != "Laptop de Moussa" || !got.LabelAt.Equal(want.LabelAt) {
		t.Fatalf("relay lists %q at %v, want the owner's %q at %v", got.CustomName, got.LabelAt, want.CustomName, want.LabelAt)
	}

	// The same labels again: nothing changes (they are what it has).
	if status, body := w.send(w.owner, "POST", RouteLabels, labels); status != http.StatusOK || len(changedOf(body)) != 0 {
		t.Fatalf("labels again = %d %s", status, body)
	}
}

func TestLabels_ARemoteRenamePersistsOnTheRelayAndReachesTheOwner(t *testing.T) {
	w := newLabelsWorld(t)
	mac := w.anyMAC()
	at := arT0.Add(time.Hour)

	// Awa (a member with control) renames it from outside, through the relay.
	if status, body := w.send(w.member, "POST", RouteLabels, labelsBody(mac, "Tablette de Awa", at)); status != http.StatusOK {
		t.Fatalf("member's rename = %d %s", status, body)
	}
	// Kept by the relay: on its next list and after the next rounds.
	for range 3 {
		w.round()
	}
	got := w.relayList()[mac]
	if got.CustomName != "Tablette de Awa" || !got.LabelAt.Equal(at) {
		t.Fatalf("relay lists %q at %v", got.CustomName, got.LabelAt)
	}

	// The owner's app reads the relay's list and gives its core the labels
	// that are newer than its own: the rename lands on the owner's phone.
	if status, body := w.call(w.ownerAPI, "POST", "/api/labels", labelsBody(mac, got.CustomName, got.LabelAt)); status != http.StatusOK {
		t.Fatalf("owner's core = %d %s", status, body)
	}
	if mine := w.devices(w.ownerAPI)[mac]; mine.CustomName != "Tablette de Awa" {
		t.Fatalf("owner's phone lists %q", mine.CustomName)
	}

	// Conflict: an older edit arriving late loses, a later one wins.
	if status, body := w.send(w.owner, "POST", RouteLabels, labelsBody(mac, "Vieux nom", at.Add(-time.Minute))); status != http.StatusOK || len(changedOf(body)) != 0 {
		t.Fatalf("stale label = %d %s", status, body)
	}
	if got := w.relayList()[mac].CustomName; got != "Tablette de Awa" {
		t.Fatalf("an older edit won on the relay: %q", got)
	}
	if status, body := w.send(w.owner, "POST", RouteLabels, labelsBody(mac, "Tablette", at.Add(time.Minute))); status != http.StatusOK || !strings.Contains(body, mac) {
		t.Fatalf("later label = %d %s", status, body)
	}
	if got := w.relayList()[mac].CustomName; got != "Tablette" {
		t.Fatalf("the later edit lost on the relay: %q", got)
	}
}

func TestLabels_ALocalRenameAtHomeReachesThePausedRelay(t *testing.T) {
	w := newLabelsWorld(t)
	mac := w.anyMAC()

	// The owner is at home: their phone holds the box, the relay pauses.
	w.box.HoldSession(ownerPhone)
	w.round()
	if st := w.agent.Status(); st.State != StatusPaused {
		t.Fatalf("status = %+v, want paused", st)
	}

	// They rename on their phone, which talks to the box directly, and the
	// app sends the label through the hub.
	if status, body := w.call(w.ownerAPI, "PATCH", "/api/devices/"+mac, `{"custom_name":"Imprimante"}`); status != http.StatusOK {
		t.Fatalf("rename = %d %s", status, body)
	}
	_, labels := w.call(w.ownerAPI, "GET", "/api/labels", "")
	logins, preempts := w.box.Logins(), w.box.Preempts()
	if status, body := w.send(w.owner, "POST", RouteLabels, labels); status != http.StatusOK {
		t.Fatalf("labels while paused = %d %s", status, body)
	}
	// Not box work: the owner keeps the box, the relay stays paused.
	if w.box.Logins() != logins || w.box.Preempts() != preempts || w.box.Holder() != ownerPhone {
		t.Fatalf("the labels touched the box: %d logins, %d preempts, holder %q",
			w.box.Logins()-logins, w.box.Preempts()-preempts, w.box.Holder())
	}
	if paused, _ := w.agent.yield.Paused(); !paused {
		t.Fatal("the labels took the box back from the owner at home")
	}
	// A member's rename is not box work either: it is not refused for the
	// owner being home, as their block would be.
	if status, body := w.send(w.member, "POST", RouteLabels, labelsBody("ac:bb:cc:00:00:77", "Console", arT0.Add(time.Hour))); status != http.StatusOK {
		t.Fatalf("member's labels while paused = %d %s", status, body)
	}

	// The relay has them: once the owner leaves, its list shows the name.
	status, body := w.call(w.relayAPI, "GET", "/api/labels", "")
	if status != http.StatusOK || !strings.Contains(body, "Imprimante") || !strings.Contains(body, "Console") {
		t.Fatalf("relay's labels = %d %s", status, body)
	}
	w.box.DropSession()
	w.clock.set(w.clock.now().Add(time.Minute))
	w.round()
	if got := w.relayList()[mac].CustomName; got != "Imprimante" {
		t.Fatalf("relay lists %q once the owner left", got)
	}
}

func TestLabels_RefusedWhenMalformedOrFromAViewKey(t *testing.T) {
	w := newLabelsWorld(t)
	mac := w.anyMAC()
	if status, _ := w.send(w.owner, "POST", RouteLabels, `{"labels":[{"mac":"`+mac+`","name":"x"}]}`); status != http.StatusBadRequest {
		t.Fatalf("a label without a time = %d, want 400", status)
	}
	if status, _ := w.send(w.owner, "POST", RouteLabels, `{"labels":[],"evil":1}`); status != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d, want 400", status)
	}
	stranger, err := trust.Generate(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := w.send(stranger, "POST", RouteLabels, labelsBody(mac, "Pirate", arT0.Add(time.Hour))); status != http.StatusForbidden {
		t.Fatalf("a key the owner never delegated = %d, want 403", status)
	}
	if got := w.relayList()[mac].CustomName; got != "" {
		t.Fatalf("a refused label landed: %q", got)
	}
}
