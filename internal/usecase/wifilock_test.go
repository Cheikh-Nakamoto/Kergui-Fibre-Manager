package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// The closed Wi-Fi's use cases (RFC-007 D2, D4, D5; #106), on the F6600P mock
// and a fake clock. The mock records every write that makes an access point
// read its list the wrong way (WiFiHazards): every test ends with none.

const (
	lkAP1, lkAP2, lkAP5 = "DEV.WIFI.AP1", "DEV.WIFI.AP2", "DEV.WIFI.AP5"

	lkPhoneIP = "192.168.1.77"
	lkPhone   = "02:aa:cc:00:00:01" // this phone
	lkRelay   = "02:aa:cc:00:00:02" // the household's relay
	lkOwner   = "02:aa:cc:00:00:03" // a co-owner's phone (Owner)
	lkLaptop  = "02:aa:cc:00:00:04" // approved
	lkTablet  = "02:aa:cc:00:00:05" // approved, then blocked
	lkGuest   = "02:aa:cc:00:00:06" // never approved
	lkDenied  = "02:aa:cc:00:00:07" // refused at the Wi-Fi before the close
)

var lockT0 = time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)

// lkHousehold is the household's relays and Owner phones.
type lkHousehold struct{ relays, owners []domain.MAC }

func (h lkHousehold) RelayMACs(context.Context, string) ([]domain.MAC, error) { return h.relays, nil }
func (h lkHousehold) OwnerMACs(context.Context, string) ([]domain.MAC, error) { return h.owners, nil }

type lockRig struct {
	box       *mockrouter.F6600P
	in        usecase.MACFilterInput
	clock     *stepClock
	log       *journal
	store     *sqlite.WiFiLockRepo
	approvals *fakeApprovals
	household lkHousehold
	uc        *usecase.WiFiLock
	// pending is the background work Open handed over, run by the test.
	pending []func()
	self    port.SelfLocator
	build   func() *usecase.WiFiLock
}

func newLockRig(t *testing.T) *lockRig {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "admin")
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &lockRig{
		box: box, clock: &stepClock{t: lockT0}, log: &journal{}, store: sqlite.NewWiFiLockRepo(db),
		approvals: &fakeApprovals{macs: map[string][]domain.MAC{}},
		household: lkHousehold{relays: []domain.MAC{domain.MustMAC(lkRelay)}, owners: []domain.MAC{domain.MustMAC(lkOwner)}},
		self:      fixedSelf{lkPhoneIP},
		in: usecase.MACFilterInput{BaseURL: srv.URL, AdapterID: "zte_f6600p",
			Creds: domain.Credentials{Username: "admin", Password: "admin"}},
	}
	factory := catalog.New()
	disco := discovery.New(factory, r.clock)
	vlt := newVault()
	devices := sqlite.NewDeviceRepo(db)
	purge := usecase.NewPurgeStaleRules(factory, vlt, disco, devices, sqlite.NewRulePurgeRepo(db), r.clock, r.log)
	r.build = func() *usecase.WiFiLock {
		return usecase.NewWiFiLock(factory, vlt, disco, r.store, r.clock, r.log).
			WithSelf(r.self).WithApprovals(r.approvals).WithHousehold(r.household).WithPurge(purge).
			WithBackground(func(f func()) { r.pending = append(r.pending, f) })
	}
	r.uc = r.build()
	r.approve(lkLaptop, lkTablet)
	box.Join(mockrouter.Station{MAC: lkPhone, IP: lkPhoneIP, AP: lkAP1})
	box.Join(mockrouter.Station{MAC: lkLaptop, IP: "192.168.1.78", AP: lkAP5})
	return r
}

func (r *lockRig) approve(macs ...string) {
	id := usecase.RouterID(r.in.BaseURL)
	r.approvals.mu.Lock()
	defer r.approvals.mu.Unlock()
	r.approvals.macs[id] = nil
	for _, m := range macs {
		r.approvals.macs[id] = append(r.approvals.macs[id], domain.MustMAC(m))
	}
}

func (r *lockRig) close(t *testing.T) usecase.WiFiLockPreview {
	t.Helper()
	p, err := r.uc.Close(context.Background(), usecase.WiFiCloseInput{MACFilterInput: r.in})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return p
}

func (r *lockRig) intent(t *testing.T) domain.WiFiLockIntent {
	t.Helper()
	i, err := r.store.WiFiLockIntent(context.Background(), usecase.RouterID(r.in.BaseURL))
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func (r *lockRig) cut(t *testing.T) []string {
	t.Helper()
	macs, err := r.store.WiFiCutMACs(context.Background(), usecase.RouterID(r.in.BaseURL))
	if err != nil {
		t.Fatal(err)
	}
	return macStrings(macs)
}

func (r *lockRig) runBackground() {
	for len(r.pending) > 0 {
		f := r.pending[0]
		r.pending = r.pending[1:]
		f()
	}
}

func (r *lockRig) modes(aps ...string) []string {
	out := make([]string, 0, len(aps))
	for _, ap := range aps {
		out = append(out, r.box.WiFiPolicy(ap))
	}
	return out
}

func (r *lockRig) noHazard(t *testing.T) {
	t.Helper()
	if h := r.box.WiFiHazards(); len(h) != 0 {
		t.Fatalf("a write read a list the wrong way: %v", h)
	}
}

// admitted reports, for each mac, whether every household AP lets it in.
func (r *lockRig) admitted(macs ...string) map[string]bool {
	out := map[string]bool{}
	for _, m := range macs {
		out[m] = r.box.Admits(lkAP1, m) && r.box.Admits(lkAP5, m)
	}
	return out
}

func (r *lockRig) wantAdmitted(t *testing.T, want map[string]bool) {
	t.Helper()
	for m, w := range want {
		if got := r.box.Admits(lkAP1, m) && r.box.Admits(lkAP5, m); got != w {
			t.Errorf("%s admitted = %v, want %v", m, got, w)
		}
	}
}

func (r *lockRig) trust(t *testing.T, mac string, to domain.DeviceTrust, cutWiFi bool) error {
	t.Helper()
	return r.uc.SetTrust(context.Background(), usecase.WiFiTrustInput{
		MACFilterInput: r.in, MAC: domain.MustMAC(mac), To: to, CutWiFi: cutWiFi,
	})
}

// --- closing -----------------------------------------------------------------

// The preview lists the target, the places and whether it fits, before any
// write: the approvals, this phone (found by its address on the box), the
// relays and the Owner phones; blocked devices are left out.
func TestWiFiLock_PreviewWritesNothing(t *testing.T) {
	r := newLockRig(t)
	r.box.AddFirewallRule(lkTablet, lkTablet) // « Couper internet »
	p, err := r.uc.Preview(context.Background(), usecase.WiFiCloseInput{MACFilterInput: r.in})
	if err != nil {
		t.Fatal(err)
	}
	if got := macStrings(p.Devices); !slices.Equal(got, []string{lkPhone, lkRelay, lkOwner, lkLaptop}) {
		t.Errorf("target = %v, want this phone, the relay, the Owner phone and the laptop", got)
	}
	if got := macStrings(p.Always); !slices.Equal(got, []string{lkPhone, lkRelay, lkOwner}) {
		t.Errorf("always = %v", got)
	}
	if p.Self.String() != lkPhone || !p.SelfPrivate {
		t.Errorf("self = %s private %v, want %s, private", p.Self, p.SelfPrivate, lkPhone)
	}
	if !slices.Equal(p.ClosedAPs, []string{lkAP1, lkAP5}) || !p.PlacesKnown || p.Places != 32 || p.Full != nil {
		t.Errorf("preview = %+v, want AP1 and AP5 closed, 32 places, fits", p)
	}
	if n := r.box.WiFiWrites(); n != 0 {
		t.Fatalf("the preview wrote %d times to the Wi-Fi", n)
	}
}

// « liste pleine » before any write: the numbers, and nothing written.
func TestWiFiLock_FullListWritesNothing(t *testing.T) {
	r := newLockRig(t)
	// The F6600P's 64 rules, 58 of them on a network that stays open: 3
	// places on the 2 closed ones, for 5 devices.
	r.box.SetRuleLimits(0, mockrouter.F6600PWiFiRuleLimit)
	for i := range 58 {
		r.box.AddWiFiRule(fmt.Sprintf("02:00:00:00:01:%02x", i), lkAP2, fmt.Sprintf("voisin %d", i))
	}
	writes := r.box.WiFiWrites()
	p, err := r.uc.Preview(context.Background(), usecase.WiFiCloseInput{MACFilterInput: r.in})
	if err != nil || p.Full == nil || p.Full.Limit != 3 || p.Full.Used != 5 {
		t.Fatalf("preview = %+v, %v; want full, 5 devices for 3 places", p.Full, err)
	}
	_, err = r.uc.Close(context.Background(), usecase.WiFiCloseInput{MACFilterInput: r.in})
	var full *domain.AccessListFullError
	if !errors.As(err, &full) || full.Limit != 3 || full.Used != 5 {
		t.Fatalf("Close = %v, want the full list", err)
	}
	if r.box.WiFiWrites() != writes || r.intent(t).Closed {
		t.Fatal("a close that does not fit wrote to the box or was recorded")
	}
	if r.log.count("liste Wi-Fi est pleine") != 1 {
		t.Errorf("journal: %v", r.log.lines)
	}
}

// Closing converges the household's networks to Allow: the listed devices
// get in, a stranger is refused; the visitors' network stays open unless
// the household closes it too.
func TestWiFiLock_Close(t *testing.T) {
	r := newLockRig(t)
	r.box.EnableAP(lkAP2, "MonWiFi-Invites")
	in := usecase.WiFiCloseInput{MACFilterInput: r.in, VisitorAPs: []string{lkAP2}}
	p, err := r.uc.Close(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.ClosedAPs, []string{lkAP1, lkAP5}) || !slices.Equal(p.VisitorAPs, []string{lkAP2}) {
		t.Fatalf("closed %v, visitors %v", p.ClosedAPs, p.VisitorAPs)
	}
	if got := r.modes(lkAP1, lkAP5, lkAP2); !slices.Equal(got, []string{"Allow", "Allow", "Disabled"}) {
		t.Fatalf("modes = %v, want the household's closed and the visitors' open", got)
	}
	r.wantAdmitted(t, map[string]bool{lkPhone: true, lkRelay: true, lkOwner: true, lkLaptop: true, lkGuest: false})
	if !r.box.Admits(lkAP2, lkGuest) {
		t.Error("the visitors' network refuses a visitor")
	}
	i := r.intent(t)
	if !i.Closed || !slices.Equal(i.HouseholdAPs, []string{lkAP1, lkAP5}) || !slices.Equal(i.VisitorAPs, []string{lkAP2}) || !i.Since.Equal(lockT0) {
		t.Fatalf("intent = %+v", i)
	}
	if r.log.count("Wi-Fi fermé") != 1 {
		t.Errorf("journal: %v", r.log.lines)
	}
	r.noHazard(t)

	// « Fermer aussi le Wi-Fi des visiteurs »; the answer is kept.
	in = usecase.WiFiCloseInput{MACFilterInput: r.in, VisitorsClosed: true}
	if _, err := r.uc.Close(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if got := r.box.WiFiPolicy(lkAP2); got != "Allow" || r.box.Admits(lkAP2, lkGuest) {
		t.Fatalf("visitors' network = %s, want closed too", got)
	}
	if i := r.intent(t); !i.VisitorsClosed || !slices.Equal(i.VisitorAPs, []string{lkAP2}) {
		t.Fatalf("intent = %+v, want the visitors' network kept", i)
	}
	r.noHazard(t)
}

// From « open, with Wi-Fi refusals »: the refusals go from the box and stay
// recorded in kergui, to be written back at the next open (D4 step 3).
func TestWiFiLock_CloseKeepsTheRefusalsInKergui(t *testing.T) {
	r := newLockRig(t)
	for _, ap := range []string{lkAP1, lkAP5} {
		r.box.SetWiFiPolicy(ap, "Ban")
		r.box.AddWiFiRule(lkDenied, ap, lkDenied+" "+strings.TrimPrefix(ap, "DEV.WIFI."))
	}
	r.close(t)
	if got := r.cut(t); !slices.Equal(got, []string{lkDenied}) {
		t.Fatalf("« déconnectés du Wi-Fi » = %v, want [%s]", got, lkDenied)
	}
	r.wantAdmitted(t, map[string]bool{lkDenied: false, lkPhone: true})
	r.noHazard(t)
}

// A close the box refuses half-way is undone: the box is back as it was,
// nothing is recorded as closed, and the journal says so.
func TestWiFiLock_CloseRollsBack(t *testing.T) {
	r := newLockRig(t)
	for _, ap := range []string{lkAP1, lkAP5} {
		r.box.SetWiFiPolicy(ap, "Ban")
		r.box.AddWiFiRule(lkDenied, ap, lkDenied+" "+strings.TrimPrefix(ap, "DEV.WIFI."))
	}
	before := r.box.WiFiRules()
	r.box.FailWiFiWrite(4) // after the switch to Disabled and the first rules
	if _, err := r.uc.Close(context.Background(), usecase.WiFiCloseInput{MACFilterInput: r.in}); err == nil {
		t.Fatal("a close the box refused succeeded")
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Ban", "Ban"}) {
		t.Fatalf("modes after the rollback = %v, want Ban as before", got)
	}
	if got := r.box.WiFiRules(); len(got) != len(before) {
		t.Fatalf("rules after the rollback = %+v, want %+v", got, before)
	}
	if r.intent(t).Closed {
		t.Fatal("a failed close was recorded as closed")
	}
	if r.log.count("Fermeture du Wi-Fi annulée") != 1 {
		t.Errorf("journal: %v", r.log.lines)
	}
	r.noHazard(t)
}

// Neither this phone nor a relay in the list: closing would lock the
// household out, so nothing is written.
func TestWiFiLock_CloseNeedsThisPhoneOrARelay(t *testing.T) {
	r := newLockRig(t)
	r.self = fixedSelf{"192.168.1.250"} // off the box's list
	r.household = lkHousehold{}
	r.uc = r.build()
	_, err := r.uc.Close(context.Background(), usecase.WiFiCloseInput{MACFilterInput: r.in})
	if !errors.Is(err, domain.ErrSelfLockout) {
		t.Fatalf("Close = %v, want ErrSelfLockout", err)
	}
	if r.box.WiFiWrites() != 0 {
		t.Fatal("the box was written")
	}
}

// --- opening -----------------------------------------------------------------

// Opening is a mode flip, the allow list kept (« open, light »); the
// refusals are written back afterwards, in the background (D4).
func TestWiFiLock_OpenThenRefusalsInTheBackground(t *testing.T) {
	r := newLockRig(t)
	for _, ap := range []string{lkAP1, lkAP5} {
		r.box.SetWiFiPolicy(ap, "Ban")
		r.box.AddWiFiRule(lkDenied, ap, lkDenied+" "+strings.TrimPrefix(ap, "DEV.WIFI."))
	}
	r.close(t)
	rulesClosed := r.box.WiFiRuleCount()

	res, err := r.uc.Open(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if got := macStrings(res.Restoring); !slices.Equal(got, []string{lkDenied}) {
		t.Fatalf("restoring = %v, want [%s]", got, lkDenied)
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Disabled", "Disabled"}) || r.box.WiFiRuleCount() != rulesClosed {
		t.Fatalf("right after Open: modes %v, %d rules; want Disabled, the allow list kept", got, r.box.WiFiRuleCount())
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: true, lkDenied: true})
	if st, err := r.uc.Status(context.Background(), r.in); err != nil || st.State != usecase.WiFiStateBusy {
		t.Fatalf("status while restoring = %+v, %v; want busy (« … En cours »)", st, err)
	}
	if r.intent(t).Closed {
		t.Fatal("the intent is still closed")
	}

	r.runBackground()
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Ban", "Ban"}) {
		t.Fatalf("after the background step: %v, want Ban", got)
	}
	r.wantAdmitted(t, map[string]bool{lkDenied: false, lkGuest: true, lkPhone: true, lkLaptop: true})
	if n := r.box.WiFiRuleCount(); n != 2 {
		t.Errorf("%d rules, want the refusal on both APs only", n)
	}
	if st, _ := r.uc.Status(context.Background(), r.in); st.State != usecase.WiFiStateOpen {
		t.Errorf("status = %q, want open", st.State)
	}
	if r.log.count("Wi-Fi ouvert") != 1 || r.log.count("Refus Wi-Fi rétablis") != 1 {
		t.Errorf("journal: %v", r.log.lines)
	}
	r.noHazard(t)
}

// With nobody « déconnecté du Wi-Fi », opening hands nothing to the
// background.
func TestWiFiLock_OpenLight(t *testing.T) {
	r := newLockRig(t)
	r.close(t)
	res, err := r.uc.Open(context.Background(), r.in)
	if err != nil || len(res.Restoring) != 0 || len(r.pending) != 0 {
		t.Fatalf("Open = %+v, %v, %d background jobs", res, err, len(r.pending))
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Disabled", "Disabled"}) {
		t.Fatalf("modes = %v", got)
	}
	r.noHazard(t)
}

// --- the add window ----------------------------------------------------------

func TestWiFiLock_AddWindowAndItsExtensions(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	if _, err := r.uc.OpenWindow(ctx, r.in); !errors.Is(err, domain.ErrWiFiNotClosed) {
		t.Fatalf("a window on an open Wi-Fi: %v", err)
	}
	r.close(t)
	w, err := r.uc.OpenWindow(ctx, r.in)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Until.Equal(lockT0.Add(5*time.Minute)) || w.MinutesLeft != 5 || !w.CanExtend {
		t.Fatalf("window = %+v", w)
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Disabled", "Disabled"}) {
		t.Fatalf("modes in the window = %v", got)
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: true})
	if st, _ := r.uc.Status(ctx, r.in); st.State != usecase.WiFiStateWindow || st.Window.MinutesLeft != 5 {
		t.Fatalf("status = %+v, want the window, 5 min", st)
	}

	r.clock.advance(2 * time.Minute)
	for _, want := range []time.Duration{10 * time.Minute, 15 * time.Minute} {
		if w, err = r.uc.ExtendWindow(ctx, r.in); err != nil {
			t.Fatal(err)
		}
		if !w.Until.Equal(lockT0.Add(want)) {
			t.Fatalf("deadline = %s, want t0+%s", w.Until, want)
		}
	}
	if w.CanExtend {
		t.Error("a 15 min window can be extended")
	}
	if _, err := r.uc.ExtendWindow(ctx, r.in); !errors.Is(err, domain.ErrWindowAtMost) {
		t.Fatalf("past 15 min: %v", err)
	}
	if !r.intent(t).WindowUntil.Equal(lockT0.Add(15 * time.Minute)) {
		t.Fatal("the deadline is not stored")
	}

	// « Refermer maintenant ».
	if err := r.uc.CloseWindow(ctx, r.in); err != nil {
		t.Fatal(err)
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Allow", "Allow"}) {
		t.Fatalf("modes after « Refermer maintenant » = %v", got)
	}
	if i := r.intent(t); !i.Closed || !i.WindowUntil.IsZero() {
		t.Fatalf("intent = %+v", i)
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: false, lkPhone: true})
	for _, line := range []string{"Fenêtre d'ajout ouverte", "Fenêtre d'ajout prolongée", "Wi-Fi refermé"} {
		if r.log.count(line) == 0 {
			t.Errorf("journal lacks %q: %v", line, r.log.lines)
		}
	}
	r.noHazard(t)
}

// The deadline is stored: the next one to reach the box after it closes the
// Wi-Fi again, a core that restarted included.
func TestWiFiLock_WindowClosedAgainByTheNextRound(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	r.close(t)
	if _, err := r.uc.OpenWindow(ctx, r.in); err != nil {
		t.Fatal(err)
	}
	r.clock.advance(4 * time.Minute)
	if tick, err := r.uc.Tick(ctx, r.in); err != nil || tick.Reclosed {
		t.Fatalf("tick at 4 min = %+v, %v", tick, err)
	}
	if got := r.box.WiFiPolicy(lkAP1); got != "Disabled" {
		t.Fatalf("closed before the deadline: %s", got)
	}

	// The app was killed: a new core, the same database.
	r.clock.advance(20 * time.Minute)
	next := r.build()
	tick, err := next.Tick(ctx, r.in)
	if err != nil || !tick.Reclosed {
		t.Fatalf("tick past the deadline = %+v, %v; want closed again", tick, err)
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Allow", "Allow"}) {
		t.Fatalf("modes = %v", got)
	}
	if !r.intent(t).WindowUntil.IsZero() {
		t.Fatal("the window is still stored")
	}
	if r.log.count("Fenêtre d'ajout terminée") != 1 {
		t.Errorf("journal: %v", r.log.lines)
	}
	r.noHazard(t)
}

// A box found open with no add window (a reset, the box's own page) is never
// closed by kergui: one journal line, and the status says « Wi-Fi ouvert ».
func TestWiFiLock_FoundOpenIsNeverClosed(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	r.close(t)
	r.box.SetWiFiPolicy(lkAP1, "Disabled")
	r.box.SetWiFiPolicy(lkAP5, "Disabled")
	writes := r.box.WiFiWrites()
	for range 3 {
		r.clock.advance(time.Hour)
		tick, err := r.uc.Tick(ctx, r.in)
		if err != nil || !tick.FoundOpen || tick.Reclosed {
			t.Fatalf("tick = %+v, %v; want found open, not closed", tick, err)
		}
	}
	if r.box.WiFiWrites() != writes || r.box.WiFiPolicy(lkAP1) != "Disabled" {
		t.Fatal("kergui closed a box it found open")
	}
	if n := r.log.count("trouvé ouvert"); n != 1 {
		t.Errorf("%d journal lines for the open box, want 1: %v", n, r.log.lines)
	}
	st, err := r.uc.Status(ctx, r.in)
	if err != nil || st.State != usecase.WiFiStateOpen || !st.FoundOpen {
		t.Fatalf("status = %+v, %v", st, err)
	}
}

// --- D5: Autoriser, À vérifier, Bloquer, Débloquer ---------------------------

// Wi-Fi open: Autoriser and À vérifier write nothing; Bloquer is the
// firewall rule; « Le déconnecter aussi du Wi-Fi » adds the refusal on each
// household AP; Débloquer removes both.
func TestWiFiLock_TrustWhileOpen(t *testing.T) {
	r := newLockRig(t)
	if err := r.trust(t, lkGuest, domain.TrustAuthorised, false); err != nil {
		t.Fatal(err)
	}
	if err := r.trust(t, lkLaptop, domain.TrustToCheck, false); err != nil {
		t.Fatal(err)
	}
	if r.box.WiFiWrites() != 0 {
		t.Fatal("Autoriser or À vérifier wrote to the Wi-Fi while open")
	}
	if err := r.trust(t, lkGuest, domain.TrustBlocked, false); err != nil {
		t.Fatal(err)
	}
	if !r.box.Blocked(lkGuest) || r.box.WiFiWrites() != 0 {
		t.Fatalf("« Couper internet »: firewall %v, %d Wi-Fi writes", r.box.Blocked(lkGuest), r.box.WiFiWrites())
	}
	if err := r.trust(t, lkGuest, domain.TrustBlocked, true); err != nil {
		t.Fatal(err)
	}
	if !r.box.WiFiBlocked(lkGuest) || !r.box.Blocked(lkGuest) || !slices.Equal(r.cut(t), []string{lkGuest}) {
		t.Fatalf("« Le déconnecter aussi du Wi-Fi »: Wi-Fi %v, firewall %v, cut %v", r.box.WiFiBlocked(lkGuest), r.box.Blocked(lkGuest), r.cut(t))
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: false, lkPhone: true, lkLaptop: true})

	if err := r.trust(t, lkGuest, domain.TrustToCheck, false); err != nil { // Débloquer
		t.Fatal(err)
	}
	if r.box.Blocked(lkGuest) || r.box.WiFiBlocked(lkGuest) || len(r.cut(t)) != 0 {
		t.Fatal("Débloquer left a rule or the flag")
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Disabled", "Disabled"}) || r.box.WiFiRuleCount() != 0 {
		t.Fatalf("after the last refusal went: %v, %d rules", got, r.box.WiFiRuleCount())
	}
	r.noHazard(t)
}

// Comment 2 of #106: on « open, light » (Disabled, the allow list kept), a
// Wi-Fi refusal goes through ApplyWiFiLock in Ban: no authorisation becomes
// a refusal. The mock notes the trap that a rule added and a switch to Ban
// (WiFiBlock) would spring on the same box.
func TestWiFiLock_RefusalOnOpenLightTurnsNoAuthorisationRound(t *testing.T) {
	openLight := func(t *testing.T) *lockRig {
		r := newLockRig(t)
		r.close(t)
		if _, err := r.uc.Open(context.Background(), r.in); err != nil {
			t.Fatal(err)
		}
		if r.box.WiFiRuleCount() == 0 {
			t.Fatal("the allow list was not kept")
		}
		return r
	}

	r := openLight(t)
	if err := r.trust(t, lkGuest, domain.TrustBlocked, true); err != nil {
		t.Fatal(err)
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: false, lkPhone: true, lkRelay: true, lkOwner: true, lkLaptop: true})
	r.noHazard(t)

	// An authorised device blocked the same way: its allow rule goes before
	// the switch, so it is not read the other way either.
	if err := r.trust(t, lkLaptop, domain.TrustBlocked, true); err != nil {
		t.Fatal(err)
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: false, lkLaptop: false, lkPhone: true})
	r.noHazard(t)

	// The legacy route (« Couper l'accès Wi-Fi ») takes the same way.
	r2 := openLight(t)
	factory := catalog.New()
	wifi := usecase.NewWiFiAccess(factory, newVault(), discovery.New(factory, r2.clock)).WithSelf(r2.self)
	if err := wifi.Set(context.Background(), r2.in, domain.MustMAC(lkGuest), true); err != nil {
		t.Fatal(err)
	}
	r2.wantAdmitted(t, map[string]bool{lkGuest: false, lkPhone: true, lkLaptop: true})
	r2.noHazard(t)

	// WiFiBlock straight on such a box: the trap, and the mock notes it.
	r3 := openLight(t)
	gw, err := factory.New("zte_f6600p", r3.in.BaseURL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Login(context.Background(), r3.in.Creds); err != nil {
		t.Fatal(err)
	}
	_ = gw.(port.WiFiAccessControl).WiFiBlock(context.Background(), domain.MustMAC(lkGuest))
	if h := r3.box.WiFiHazards(); len(h) == 0 || !strings.Contains(strings.Join(h, " "), "authorisation") {
		t.Fatalf("hazards = %v, want the authorisations read as refusals", h)
	}
	if r3.box.Admits(lkAP1, lkPhone) {
		t.Fatal("the trap: this phone is refused, as the mock should show")
	}
}

// Wi-Fi closed: Autoriser adds the allow rule, À vérifier removes it,
// Bloquer is the firewall rule and the allow rule removed, « Le déconnecter
// aussi du Wi-Fi » is kept in kergui only, Débloquer forgets it and the
// device stays refused until authorised.
func TestWiFiLock_TrustWhileClosed(t *testing.T) {
	r := newLockRig(t)
	r.close(t)
	r.approve(lkLaptop, lkTablet, lkGuest) // the app records the approval
	if err := r.trust(t, lkGuest, domain.TrustAuthorised, false); err != nil {
		t.Fatal(err)
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: true})
	r.approve(lkTablet, lkGuest)
	if err := r.trust(t, lkLaptop, domain.TrustToCheck, false); err != nil {
		t.Fatal(err)
	}
	r.wantAdmitted(t, map[string]bool{lkLaptop: false, lkGuest: true, lkPhone: true})

	if err := r.trust(t, lkTablet, domain.TrustBlocked, true); err != nil {
		t.Fatal(err)
	}
	if !r.box.Blocked(lkTablet) || r.box.Admits(lkAP1, lkTablet) || !slices.Equal(r.cut(t), []string{lkTablet}) {
		t.Fatalf("blocked while closed: firewall %v, admitted %v, cut %v", r.box.Blocked(lkTablet), r.box.Admits(lkAP1, lkTablet), r.cut(t))
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Allow", "Allow"}) {
		t.Fatalf("modes = %v, want still closed", got)
	}
	r.approve(lkGuest)
	if err := r.trust(t, lkTablet, domain.TrustToCheck, false); err != nil { // Débloquer
		t.Fatal(err)
	}
	if r.box.Blocked(lkTablet) || len(r.cut(t)) != 0 || r.box.Admits(lkAP1, lkTablet) {
		t.Fatal("Débloquer while closed: want the firewall rule gone, the flag forgotten, still refused")
	}
	r.noHazard(t)
}

// During an add window, « Autoriser » writes the allow rule with the APs
// left open; the re-close then admits the new device.
func TestWiFiLock_AuthoriseDuringTheWindow(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	r.close(t)
	if _, err := r.uc.OpenWindow(ctx, r.in); err != nil {
		t.Fatal(err)
	}
	r.approve(lkLaptop, lkTablet, lkGuest)
	if err := r.trust(t, lkGuest, domain.TrustAuthorised, false); err != nil {
		t.Fatal(err)
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Disabled", "Disabled"}) {
		t.Fatalf("modes = %v, want the window kept open", got)
	}
	if err := r.uc.CloseWindow(ctx, r.in); err != nil {
		t.Fatal(err)
	}
	r.wantAdmitted(t, map[string]bool{lkGuest: true, lkPhone: true})
	r.noHazard(t)
}

// This phone can be neither Bloqué, nor « À vérifier » while the Wi-Fi is
// closed.
func TestWiFiLock_ThisPhoneIsNeverCut(t *testing.T) {
	r := newLockRig(t)
	var lock *domain.SelfLockoutError
	if err := r.trust(t, lkPhone, domain.TrustBlocked, false); !errors.As(err, &lock) {
		t.Fatalf("block this phone while open: %v", err)
	}
	if err := r.trust(t, lkPhone, domain.TrustToCheck, false); err != nil {
		t.Fatalf("« À vérifier » on this phone while open: %v", err)
	}
	r.close(t)
	if err := r.trust(t, lkPhone, domain.TrustToCheck, false); !errors.As(err, &lock) {
		t.Fatalf("« À vérifier » on this phone while closed: %v", err)
	}
	if err := r.trust(t, lkPhone, domain.TrustBlocked, true); !errors.As(err, &lock) {
		t.Fatalf("block this phone while closed: %v", err)
	}
	if r.box.Blocked(lkPhone) || !r.box.Admits(lkAP1, lkPhone) {
		t.Fatal("this phone was cut off")
	}
}

// Comment 1 of #106: a Wi-Fi refusal the full table refuses first purges
// the stale refusals, then is tried again, as the firewall block is.
func TestWiFiAccess_FullTablePurgesThenRetries(t *testing.T) {
	r := newPurgeRig(t)
	const (
		stale1 = "02:aa:bb:00:00:31" // away 30 days
		stale2 = "02:aa:bb:00:00:32"
		recent = "02:aa:bb:00:00:33" // seen yesterday
		target = "02:aa:bb:00:00:34" // to refuse now
	)
	for _, ap := range []string{"AP1", "AP5"} {
		r.box.SetWiFiPolicy("DEV.WIFI."+ap, "Ban")
		for _, m := range []string{stale1, stale2, recent} {
			r.box.AddWiFiRule(m, "DEV.WIFI."+ap, m+" "+ap)
		}
	}
	r.box.SetRuleLimits(0, 6)
	r.seen(t, stale1, 30*day)
	r.seen(t, stale2, 40*day)
	r.seen(t, recent, day)
	wifi := usecase.NewWiFiAccess(r.factory, r.vault, r.disco).WithPurge(r.purge)
	if err := wifi.Set(context.Background(), r.in, mustMAC(t, target), true); err != nil {
		t.Fatalf("refusal with a full table: %v", err)
	}
	if !r.box.WiFiBlocked(target) || !r.box.WiFiBlocked(recent) {
		t.Fatal("the device was not refused, or a recent refusal went")
	}
	if r.box.WiFiBlocked(stale1) || r.box.WiFiBlocked(stale2) {
		t.Fatal("the stale refusals are still there")
	}
	if n := r.log.count("Règle retirée : appareil absent depuis 30 jours"); n != 2 {
		t.Errorf("%d removals in the journal, want 2: %v", n, r.log.lines)
	}
	if h := r.box.WiFiHazards(); len(h) != 0 {
		t.Fatalf("hazards: %v", h)
	}

	// Without the purge, the full answer is returned as is.
	r2 := newPurgeRig(t)
	for _, ap := range []string{"AP1", "AP5"} {
		r2.box.SetWiFiPolicy("DEV.WIFI."+ap, "Ban")
		for _, m := range []string{stale1, stale2, recent} {
			r2.box.AddWiFiRule(m, "DEV.WIFI."+ap, m+" "+ap)
		}
	}
	r2.box.SetRuleLimits(0, 6)
	err := usecase.NewWiFiAccess(r2.factory, r2.vault, r2.disco).Set(context.Background(), r2.in, mustMAC(t, target), true)
	if !errors.Is(err, domain.ErrAccessListFull) {
		t.Fatalf("without the purge: %v, want the full list", err)
	}
}

// RFC-007 D11: a box without an allow list cannot be closed; on it, a block
// is the firewall rule and « Le déconnecter aussi du Wi-Fi » its own Wi-Fi
// refusal, which Débloquer removes.
func TestWiFiLock_BoxWithoutAnAllowList(t *testing.T) {
	gw := &wifiACLGateway{fakeGateway: &fakeGateway{meta: f660Meta()}}
	gw.join(ownPhone, phoneIP, "Pixel")
	in := usecase.MACFilterInput{BaseURL: "http://192.168.1.1", AdapterID: "zte_f660", Creds: domain.Credentials{Username: "admin", Password: "x"}}
	uc := usecase.NewWiFiLock(fakeFactory{gw: gw.fakeGateway, rp: gw}, newVault(), fakeDisco{}, &memWiFiLock{},
		&stepClock{t: lockT0}, nopLogger{}).WithSelf(fixedSelf{phoneIP})
	if _, err := uc.Close(context.Background(), usecase.WiFiCloseInput{MACFilterInput: in}); !errors.Is(err, domain.ErrNotImplemented) {
		t.Fatalf("Close = %v, want ErrNotImplemented", err)
	}
	mac := domain.MustMAC(stranger)
	if err := uc.SetTrust(context.Background(), usecase.WiFiTrustInput{MACFilterInput: in, MAC: mac, To: domain.TrustBlocked, CutWiFi: true}); err != nil {
		t.Fatal(err)
	}
	if gw.refusals(stranger) != 1 || len(gw.wifiRefused) != 1 {
		t.Fatalf("firewall %d, Wi-Fi %v; want one of each", gw.refusals(stranger), gw.wifiRefused)
	}
	gw.rules = []domain.AccessRule{{MAC: mac, Mode: domain.AccessBlock}}
	if err := uc.SetTrust(context.Background(), usecase.WiFiTrustInput{MACFilterInput: in, MAC: mac, To: domain.TrustToCheck}); err != nil {
		t.Fatalf("Débloquer: %v", err)
	}
	if gw.unblocked != 1 || gw.wifiUnblocked != 1 {
		t.Fatalf("Débloquer removed %d firewall rule(s) and %d Wi-Fi refusal(s), want 1 of each", gw.unblocked, gw.wifiUnblocked)
	}
	if err := uc.SetTrust(context.Background(), usecase.WiFiTrustInput{MACFilterInput: in, MAC: domain.MustMAC(ownPhone), To: domain.TrustBlocked}); err == nil {
		t.Fatal("this phone was blocked")
	}
}
