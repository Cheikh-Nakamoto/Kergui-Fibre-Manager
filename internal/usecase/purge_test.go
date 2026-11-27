package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef6600p"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// RFC-007 D6 (#102, #103): the purge of stale rules, on the F6600P mock and a
// fake clock.

var purgeNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// journal records the log lines, as the app's journal screen shows them.
type journal struct {
	mu    sync.Mutex
	lines []string
}

func (j *journal) add(f string, a ...any) {
	j.mu.Lock()
	j.lines = append(j.lines, fmt.Sprintf(f, a...))
	j.mu.Unlock()
}
func (j *journal) Debugf(string, ...any)    {}
func (j *journal) Infof(f string, a ...any) { j.add(f, a...) }
func (j *journal) Warnf(f string, a ...any) { j.add(f, a...) }
func (j *journal) Errorf(f string, a ...any) {
	j.add(f, a...)
}

func (j *journal) count(sub string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	n := 0
	for _, l := range j.lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

type purgeRig struct {
	box     *mockrouter.F6600P
	in      usecase.MACFilterInput
	devices *sqlite.DeviceRepo
	clock   *fakeClock
	log     *journal
	purge   *usecase.PurgeStaleRules
	block   *usecase.BlockDevice
}

func newPurgeRig(t *testing.T) *purgeRig {
	t.Helper()
	return newPurgeRigWith(t, catalog.New())
}

// newPurgeRigWith builds the rig on factory, for a box model whose endpoints
// say something else than the F6600P's.
func newPurgeRigWith(t *testing.T, factory *router.Factory) *purgeRig {
	t.Helper()
	box := mockrouter.NewF6600P("admin", "admin")
	srv := httptest.NewServer(box.Handler())
	t.Cleanup(srv.Close)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clk := &fakeClock{t: purgeNow}
	disco := discovery.New(factory, clk)
	vlt := newVault()
	log := &journal{}
	r := &purgeRig{
		box: box, clock: clk, log: log, devices: sqlite.NewDeviceRepo(db),
		in: usecase.MACFilterInput{BaseURL: srv.URL, AdapterID: "zte_f6600p",
			Creds: domain.Credentials{Username: "admin", Password: "admin"}},
	}
	r.purge = usecase.NewPurgeStaleRules(factory, vlt, disco, r.devices, sqlite.NewRulePurgeRepo(db), clk, log)
	r.block = usecase.NewBlockDevice(factory, vlt, disco, clk, log).WithPurge(r.purge)
	return r
}

// seen puts mac in the inventory, last seen ago before now.
func (r *purgeRig) seen(t *testing.T, mac string, ago time.Duration) {
	t.Helper()
	m := mustMAC(t, mac)
	routerID := usecase.RouterID(r.in.BaseURL)
	at := purgeNow.Add(-ago)
	if _, err := r.devices.Upsert(context.Background(), domain.Device{
		ID: usecase.DeviceID(routerID, m), RouterID: routerID, MAC: m,
		FirstSeen: at.Add(-day), LastSeen: at,
	}); err != nil {
		t.Fatal(err)
	}
}

func (r *purgeRig) firewallMACs() []string {
	var out []string
	for _, rule := range r.box.FirewallRules() {
		out = append(out, strings.ToLower(rule.MAC))
	}
	return out
}

func mustMAC(t *testing.T, s string) domain.MAC {
	t.Helper()
	m, err := domain.ParseMAC(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func macStrings(ms []domain.MAC) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.String())
	}
	return out
}

// TestPurge_Firewall: a rule kergui wrote goes once its device has been away
// 30 days to the second; one second less, it stays. A MAC the inventory does
// not know is never removed (a reinstalled app has an empty inventory), and
// a rule the household made in the box's page is never touched.
func TestPurge_Firewall(t *testing.T) {
	r := newPurgeRig(t)
	const (
		thirty   = "02:aa:bb:00:00:01" // kergui's, away 30 days exactly
		almost   = "02:aa:bb:00:00:02" // kergui's, away 30 days less 1 s
		unknown  = "02:aa:bb:00:00:03" // kergui's, never in the inventory
		theirs   = "02:aa:bb:00:00:04" // the household's, away 90 days
		yearsAgo = "02:aa:bb:00:00:05" // kergui's, away a year
	)
	for _, m := range []string{thirty, almost, unknown, yearsAgo} {
		r.box.AddFirewallRule(m, m)
	}
	r.box.AddFirewallRule(theirs, "Console salon")
	r.seen(t, thirty, 30*day)
	r.seen(t, almost, 30*day-time.Second)
	r.seen(t, theirs, 90*day)
	r.seen(t, yearsAgo, 365*day)
	before := len(r.box.FirewallRules())

	rep, err := r.purge.Run(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if got := macStrings(rep.Removed); !slices.Equal(got, []string{thirty, yearsAgo}) {
		t.Errorf("removed %v, want [%s %s]", got, thirty, yearsAgo)
	}
	left := r.firewallMACs()
	for _, m := range []string{almost, unknown, theirs} {
		if !slices.Contains(left, m) {
			t.Errorf("%s was removed", m)
		}
	}
	if len(left) != before-2 {
		t.Errorf("%d rules left, want %d", len(left), before-2)
	}
	if n := r.log.count("Règle retirée : appareil absent depuis 30 jours"); n != 2 {
		t.Errorf("%d journal lines for the removals, want 2: %v", n, r.log.lines)
	}
}

// wifiMACs is the set of MACs the box's Wi-Fi table holds a rule for.
func (r *purgeRig) wifiMACs() map[string]bool {
	out := map[string]bool{}
	for _, rule := range r.box.WiFiRules() {
		out[strings.ToLower(rule.MAC)] = true
	}
	return out
}

// TestPurge_WiFiRefusals: a device the Wi-Fi refuses and seen in the last 30
// days gets the firewall rule it lacks; the refusals of one away 30 days go,
// on every access point (#103); one the inventory does not know is left
// alone.
func TestPurge_WiFiRefusals(t *testing.T) {
	r := newPurgeRig(t)
	const (
		recent  = "02:aa:bb:00:00:11"
		stale   = "02:aa:bb:00:00:12"
		unknown = "02:aa:bb:00:00:13"
		both    = "02:aa:bb:00:00:14" // refused and already firewall-blocked
	)
	for _, ap := range []string{"AP1", "AP5"} {
		r.box.SetWiFiPolicy("DEV.WIFI."+ap, "Ban")
		for _, m := range []string{recent, stale, unknown, both} {
			r.box.AddWiFiRule(m, "DEV.WIFI."+ap, m+" "+ap)
		}
	}
	r.box.AddFirewallRule(both, both)
	r.seen(t, recent, 3*day)
	r.seen(t, stale, 30*day)
	r.seen(t, both, day)

	rep, err := r.purge.Run(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if got := macStrings(rep.Added); !slices.Equal(got, []string{recent}) {
		t.Errorf("firewall rules added for %v, want [%s]", got, recent)
	}
	if !r.box.Blocked(recent) {
		t.Errorf("%s has no firewall rule", recent)
	}
	if r.box.Blocked(stale) || r.box.Blocked(unknown) {
		t.Error("a firewall rule was added for a device away 30 days or unknown")
	}
	if got := macStrings(rep.RemovedWiFi); !slices.Equal(got, []string{stale}) {
		t.Errorf("Wi-Fi refusals removed %v, want [%s]", got, stale)
	}
	if len(rep.StaleWiFi) != 0 {
		t.Errorf("stale Wi-Fi refusals kept: %v", rep.StaleWiFi)
	}
	left := r.wifiMACs()
	if left[stale] {
		t.Errorf("%s is still refused on an access point: %v", stale, r.box.WiFiRules())
	}
	for _, m := range []string{recent, unknown, both} {
		if !left[m] {
			t.Errorf("the Wi-Fi refusal of %s was removed", m)
		}
	}
	if got := r.box.WiFiRuleCount(); got != 6 {
		t.Errorf("%d Wi-Fi rules left, want 6 (3 MACs x 2 APs)", got)
	}
	if !r.box.MACFilterEnabled() {
		t.Error("the firewall filter is off: the added rule does nothing")
	}
	if n := r.log.count("Règle retirée : appareil absent depuis 30 jours"); n != 1 {
		t.Errorf("%d journal lines for the Wi-Fi removal, want 1: %v", n, r.log.lines)
	}
}

// TestPurge_CapturedTable: the 63 rules read on a real box (21 MACs x 3 APs,
// all kergui's). Only the rules of the devices the inventory has seen away
// 30 days go, on the 3 APs; the rest of the table is untouched, whether the
// box renumbers its rules after a delete or leaves holes (RFC-007 R6).
func TestPurge_CapturedTable(t *testing.T) {
	for _, holes := range []bool{false, true} {
		name := "renumbered"
		if holes {
			name = "holes"
		}
		t.Run(name, func(t *testing.T) {
			r := newPurgeRig(t)
			r.box.LoadCapturedWiFiACL()
			if holes {
				r.box.KeepWiFiRuleIDs()
			}
			before := r.box.WiFiRules()
			if len(before) != 63 {
				t.Fatalf("captured table has %d rules, want 63", len(before))
			}
			away := []string{"02:aa:bb:00:00:02", "02:aa:bb:00:00:05", "02:aa:bb:00:00:0e", "02:aa:bb:00:00:10", "ac:bb:cc:00:01:15"}
			for i, m := range away {
				r.seen(t, m, 30*day+time.Duration(i)*40*day)
			}
			r.seen(t, "02:aa:bb:00:00:01", 2*day)
			r.seen(t, "02:aa:bb:00:00:03", 30*day-time.Second)

			rep, err := r.purge.Run(context.Background(), r.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := macStrings(rep.RemovedWiFi); !slices.Equal(got, away) {
				t.Errorf("removed %v, want %v", got, away)
			}
			if got := macStrings(rep.Added); !slices.Equal(got, []string{"02:aa:bb:00:00:01", "02:aa:bb:00:00:03"}) {
				t.Errorf("added %v", got)
			}
			gone := map[string]bool{}
			for _, m := range away {
				gone[m] = true
			}
			var want []mockrouter.WiFiRule
			for _, rule := range before {
				if !gone[strings.ToLower(rule.MAC)] {
					want = append(want, rule)
				}
			}
			if got := r.box.WiFiRules(); !slices.Equal(got, want) {
				t.Errorf("table after the purge:\n got %v\nwant %v", got, want)
			}
			if len(want) != 63-3*len(away) {
				t.Errorf("%d rules left, want %d", len(want), 63-3*len(away))
			}
			if n := r.log.count("Règle retirée : appareil absent depuis 30 jours"); n != len(away) {
				t.Errorf("%d journal lines, want %d", n, len(away))
			}
		})
	}
}

// TestPurge_WiFiNeverRemoved: the purge leaves the Wi-Fi refusals of a device
// on the box right now, of a MAC the inventory does not know, of a rule the
// household named in the box's page, and of a MAC that also has a rule of the
// household, in either list.
func TestPurge_WiFiNeverRemoved(t *testing.T) {
	r := newPurgeRig(t)
	st := r.box.Stations()
	if len(st) == 0 {
		t.Fatal("the mock has no station")
	}
	here := strings.ToLower(st[0].MAC)
	const (
		unknown    = "02:aa:bb:00:00:71" // kergui's, never in the inventory
		theirs     = "02:aa:bb:00:00:72" // named by the household
		mixed      = "02:aa:bb:00:00:73" // kergui's on AP1, the household's on AP5
		fwTheirs   = "02:aa:bb:00:00:74" // kergui's Wi-Fi rules, the household's firewall rule
		wifiTheirs = "02:aa:bb:00:00:75" // kergui's firewall rule, the household's Wi-Fi rule
	)
	for _, ap := range []string{"AP1", "AP5"} {
		r.box.SetWiFiPolicy("DEV.WIFI."+ap, "Ban")
		for _, m := range []string{here, unknown, fwTheirs} {
			r.box.AddWiFiRule(m, "DEV.WIFI."+ap, m+" "+ap)
		}
		r.box.AddWiFiRule(theirs, "DEV.WIFI."+ap, "Tablette "+ap)
		r.box.AddWiFiRule(wifiTheirs, "DEV.WIFI."+ap, "Voisin "+ap)
	}
	r.box.AddWiFiRule(mixed, "DEV.WIFI.AP1", mixed+" AP1")
	r.box.AddWiFiRule(mixed, "DEV.WIFI.AP5", "Console")
	r.box.AddFirewallRule(fwTheirs, "Console salon")
	r.box.AddFirewallRule(wifiTheirs, wifiTheirs)
	for _, m := range []string{here, theirs, mixed, fwTheirs, wifiTheirs} {
		r.seen(t, m, 60*day)
	}
	wifiRules, fwRules := r.box.WiFiRules(), r.box.FirewallRules()

	rep, err := r.purge.Run(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RemovedWiFi) != 0 || len(rep.Removed) != 0 {
		t.Errorf("removed Wi-Fi %v, firewall %v: want nothing", rep.RemovedWiFi, rep.Removed)
	}
	if got := r.box.WiFiRules(); !slices.Equal(got, wifiRules) {
		t.Errorf("the Wi-Fi table changed:\n got %v\nwant %v", got, wifiRules)
	}
	if got := r.box.FirewallRules(); len(got) < len(fwRules) {
		t.Errorf("a firewall rule was removed: %v", got)
	}
	if r.box.WiFiWrites() != 0 {
		t.Errorf("%d Wi-Fi writes, want none", r.box.WiFiWrites())
	}
}

// unverifiedWiFi is the F6600P adapter with its wifi_unblock endpoint not
// validated on a real box, as any model before its live session.
type unverifiedWiFi struct{ *ztef6600p.Gateway }

func (g unverifiedWiFi) Meta() domain.AdapterMeta { return unverifiedWiFiMeta() }

func unverifiedWiFiMeta() domain.AdapterMeta {
	m := ztef6600p.Meta()
	m.Endpoints = slices.Clone(m.Endpoints)
	for i := range m.Endpoints {
		if m.Endpoints[i].Operation == "wifi_unblock" {
			m.Endpoints[i].Verified = false
		}
	}
	return m
}

// TestPurge_WiFiUnblockUnverified: on a model whose wifi_unblock has not run
// on a real box, the stale refusals are only listed, as before #103, and
// nothing is written to the Wi-Fi.
func TestPurge_WiFiUnblockUnverified(t *testing.T) {
	f := catalog.New()
	f.Register(unverifiedWiFiMeta(), func(baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
		gw, err := ztef6600p.New(baseURL, opts)
		if err != nil {
			return nil, err
		}
		return unverifiedWiFi{gw}, nil
	})
	r := newPurgeRigWith(t, f)
	r.box.LoadCapturedWiFiACL()
	r.seen(t, "02:aa:bb:00:00:02", 31*day)

	rep, err := r.purge.Run(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.RemovedWiFi) != 0 {
		t.Errorf("removed %v on an unverified model", rep.RemovedWiFi)
	}
	if got := macStrings(rep.StaleWiFi); !slices.Equal(got, []string{"02:aa:bb:00:00:02"}) {
		t.Errorf("stale %v", got)
	}
	if r.box.WiFiWrites() != 0 || r.box.WiFiRuleCount() != 63 {
		t.Errorf("the purge wrote to the Wi-Fi: %d writes, %d rules", r.box.WiFiWrites(), r.box.WiFiRuleCount())
	}
	if r.log.count("Refus Wi-Fi gardé") != 1 {
		t.Errorf("the kept refusal is not in the journal: %v", r.log.lines)
	}
}

// TestPurge_PresentIsNotAbsent: a device on the box right now keeps its rule,
// whatever an inventory the app did not refresh says.
func TestPurge_PresentIsNotAbsent(t *testing.T) {
	r := newPurgeRig(t)
	st := r.box.Stations()
	if len(st) == 0 {
		t.Fatal("the mock has no station")
	}
	here := strings.ToLower(st[0].MAC)
	r.box.AddFirewallRule(here, here)
	r.seen(t, here, 40*day)
	rep, err := r.purge.Run(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 0 || !r.box.Blocked(here) {
		t.Fatalf("removed %v: a device on the box lost its rule", rep.Removed)
	}
}

// TestPurge_Daily: the purge runs once a day.
func TestPurge_Daily(t *testing.T) {
	r := newPurgeRig(t)
	ctx := context.Background()
	if _, ran, err := r.purge.RunIfDue(ctx, r.in); err != nil || !ran {
		t.Fatalf("first run: ran=%v err=%v", ran, err)
	}
	r.clock.t = purgeNow.Add(23 * time.Hour)
	if _, ran, err := r.purge.RunIfDue(ctx, r.in); err != nil || ran {
		t.Fatalf("23 h later: ran=%v err=%v, want skipped", ran, err)
	}
	r.clock.t = purgeNow.Add(24 * time.Hour)
	if _, ran, err := r.purge.RunIfDue(ctx, r.in); err != nil || !ran {
		t.Fatalf("a day later: ran=%v err=%v", ran, err)
	}
}

// TestBlock_FullPurgesThenRetries: a block refused because the list is full
// first frees the rules of the devices away 30 days, then tries again.
func TestBlock_FullPurgesThenRetries(t *testing.T) {
	r := newPurgeRig(t)
	const stale, newcomer = "02:aa:bb:00:00:21", "02:aa:bb:00:00:22"
	r.box.AddFirewallRule(stale, stale)
	r.seen(t, stale, 45*day)
	r.box.SetRuleLimits(len(r.box.FirewallRules()), 0)
	// A stale Wi-Fi refusal waits for the daily purge: removing it frees no
	// room for this block, only time lost.
	r.box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
	r.box.AddWiFiRule(stale, "DEV.WIFI.AP1", stale+" AP1")

	err := r.block.Execute(context.Background(), usecase.ChangeAccessInput{
		BaseURL: r.in.BaseURL, AdapterID: r.in.AdapterID, Creds: r.in.Creds, MAC: mustMAC(t, newcomer), Confirm: true,
	})
	if err != nil {
		t.Fatalf("Block = %v, want it to fit after the purge", err)
	}
	if !r.box.Blocked(newcomer) || r.box.Blocked(stale) {
		t.Errorf("newcomer blocked=%v, stale blocked=%v", r.box.Blocked(newcomer), r.box.Blocked(stale))
	}
	if r.log.count("Règle retirée : appareil absent depuis 30 jours") != 1 {
		t.Errorf("journal: %v", r.log.lines)
	}
	if r.box.WiFiWrites() != 0 {
		t.Errorf("%d Wi-Fi writes before the block, want none", r.box.WiFiWrites())
	}
}

// TestBlock_FullWithNothingToPurge: nothing stale, the list stays full and the
// caller is told so, with the numbers.
func TestBlock_FullWithNothingToPurge(t *testing.T) {
	r := newPurgeRig(t)
	used := len(r.box.FirewallRules())
	r.box.SetRuleLimits(used, 0)
	err := r.block.Execute(context.Background(), usecase.ChangeAccessInput{
		BaseURL: r.in.BaseURL, AdapterID: r.in.AdapterID, Creds: r.in.Creds, MAC: mustMAC(t, "02:aa:bb:00:00:31"), Confirm: true,
	})
	var full *domain.AccessListFullError
	if !errors.As(err, &full) || full.Kind != domain.AccessListFirewall || full.Limit != used {
		t.Fatalf("Block = %v, want firewall full at %d", err, used)
	}
}

var _ port.Logger = (*journal)(nil)

// TestPurge_BlockedList: every device the box's lists hold, with what the
// inventory knows of it, for the « liste pleine » screen.
func TestPurge_BlockedList(t *testing.T) {
	r := newPurgeRig(t)
	const away, refused = "02:aa:bb:00:00:41", "02:aa:bb:00:00:42"
	r.box.AddFirewallRule(away, away)
	r.seen(t, away, 40*day)
	r.box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
	r.box.AddWiFiRule(refused, "DEV.WIFI.AP1", refused+" AP1")

	list, err := r.purge.Blocked(context.Background(), r.in)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]usecase.BlockedDevice{}
	for _, b := range list {
		by[b.MAC.String()] = b
	}
	a, ok := by[away]
	if !ok || !a.Firewall || a.WiFi || a.Present || !a.ByKergui || !a.LastSeen.Equal(purgeNow.Add(-40*day)) {
		t.Errorf("%s = %+v (listed %v)", away, a, ok)
	}
	w, ok := by[refused]
	if !ok || w.Firewall || !w.WiFi || !w.LastSeen.IsZero() {
		t.Errorf("%s = %+v (listed %v)", refused, w, ok)
	}
	// The household's captured rules are there too, not kergui's.
	for _, fw := range r.box.FirewallRules() {
		if fw.Name != fw.MAC && by[strings.ToLower(fw.MAC)].ByKergui {
			t.Errorf("%s (%s) reported as kergui's", fw.MAC, fw.Name)
		}
	}
	if r.box.WiFiWrites() != 0 {
		t.Error("listing wrote to the Wi-Fi")
	}
}
