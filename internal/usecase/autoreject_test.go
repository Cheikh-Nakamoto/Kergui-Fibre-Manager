package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// --- fakes for auto-reject ------------------------------------------------

// stepClock is a fake clock a test moves forward.
type stepClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *stepClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *stepClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// wifiACLGateway is a box with a firewall and a Wi-Fi access control: it
// records every firewall rule (« Couper internet ») it is asked for, which
// is how auto-reject refuses since RFC-007 D5, and every Wi-Fi refusal,
// which it no longer asks for.
type wifiACLGateway struct {
	*fakeGateway
	mu          sync.Mutex
	refused     []domain.MAC // firewall rules
	wifiRefused []domain.MAC
	blockErr    error
	// unblocked and wifiUnblocked count Unblock and WiFiUnblock.
	unblocked, wifiUnblocked int
}

func (g *wifiACLGateway) Block(_ context.Context, mac domain.MAC) error {
	if g.blockErr != nil {
		return g.blockErr
	}
	g.mu.Lock()
	g.refused = append(g.refused, mac)
	g.mu.Unlock()
	return nil
}

func (g *wifiACLGateway) WiFiBlockedMACs(context.Context) ([]domain.MAC, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]domain.MAC(nil), g.wifiRefused...), nil
}
func (g *wifiACLGateway) WiFiBlock(_ context.Context, mac domain.MAC) error {
	g.mu.Lock()
	g.wifiRefused = append(g.wifiRefused, mac)
	g.mu.Unlock()
	return nil
}
func (g *wifiACLGateway) WiFiUnblock(context.Context, domain.MAC) error {
	g.mu.Lock()
	g.wifiUnblocked++
	g.mu.Unlock()
	return nil
}
func (g *wifiACLGateway) Unblock(context.Context, domain.MAC) error {
	g.mu.Lock()
	g.unblocked++
	g.mu.Unlock()
	return nil
}

func (g *wifiACLGateway) join(mac, ip, host string) {
	g.devices = append(g.devices, domain.Device{MAC: domain.MustMAC(mac), IP: domain.MustIP(ip), Hostname: host, Connected: true})
}

func (g *wifiACLGateway) refusals(mac string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, m := range g.refused {
		if m == domain.MustMAC(mac) {
			n++
		}
	}
	return n
}

// memAutoReject is an in-memory port.AutoRejectRepository.
type memAutoReject struct {
	settings map[string]domain.AutoRejectSettings
	devices  map[string]map[domain.MAC]domain.AutoRejectDevice
}

func newMemAutoReject() *memAutoReject {
	return &memAutoReject{settings: map[string]domain.AutoRejectSettings{}, devices: map[string]map[domain.MAC]domain.AutoRejectDevice{}}
}
func (m *memAutoReject) AutoRejectSettings(_ context.Context, id string) (domain.AutoRejectSettings, error) {
	return m.settings[id], nil
}
func (m *memAutoReject) SaveAutoRejectSettings(_ context.Context, id string, s domain.AutoRejectSettings) error {
	m.settings[id] = s
	return nil
}
func (m *memAutoReject) AutoRejectDevices(_ context.Context, id string) (map[domain.MAC]domain.AutoRejectDevice, error) {
	out := map[domain.MAC]domain.AutoRejectDevice{}
	for k, v := range m.devices[id] {
		out[k] = v
	}
	return out, nil
}
func (m *memAutoReject) SaveAutoRejectDevice(_ context.Context, id string, d domain.AutoRejectDevice) error {
	if m.devices[id] == nil {
		m.devices[id] = map[domain.MAC]domain.AutoRejectDevice{}
	}
	m.devices[id][d.MAC] = d
	return nil
}
func (m *memAutoReject) ClearAutoRejectDevices(_ context.Context, id string) error {
	delete(m.devices, id)
	return nil
}

// fixedSelf is the phone's own address on the box's network.
type fixedSelf struct{ ip string }

func (s fixedSelf) SelfIP(context.Context, string) domain.IP { return domain.MustIP(s.ip) }

// lines is a logger that keeps what it is told.
type lines struct {
	mu  sync.Mutex
	all []string
}

func (l *lines) add(f string, a ...any) {
	l.mu.Lock()
	l.all = append(l.all, fmt.Sprintf(f, a...))
	l.mu.Unlock()
}
func (l *lines) Debugf(f string, a ...any) { l.add(f, a...) }
func (l *lines) Infof(f string, a ...any)  { l.add(f, a...) }
func (l *lines) Warnf(f string, a ...any)  { l.add(f, a...) }
func (l *lines) Errorf(f string, a ...any) { l.add(f, a...) }
func (l *lines) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.all {
		if strings.Contains(s, sub) {
			n++
		}
	}
	return n
}

const (
	phoneIP   = "192.168.1.20"
	presentA  = "aa:00:00:00:00:01" // on the box when the rule is turned on
	ownPhone  = "aa:00:00:00:00:02" // the phone running the app (random MAC)
	stranger  = "bb:00:00:00:00:01" // joins later
	stranger2 = "bb:00:00:00:00:02"
)

type autoRejectFixture struct {
	gw      *wifiACLGateway
	uc      *usecase.AutoReject
	in      usecase.MACFilterInput
	clock   *stepClock
	devices *fakeDeviceRepo
	log     *lines
}

var autoRejectT0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

// autoRejectSetup is a box with one device and the phone on it, the rule
// not yet on.
func autoRejectSetup(t *testing.T) *autoRejectFixture {
	t.Helper()
	gw := &wifiACLGateway{fakeGateway: &fakeGateway{meta: f660Meta()}}
	gw.join(presentA, "192.168.1.10", "Laptop")
	gw.join(ownPhone, phoneIP, "Pixel")
	f := &autoRejectFixture{
		gw:      gw,
		in:      usecase.MACFilterInput{BaseURL: "http://192.168.1.1", AdapterID: "zte_f660", Creds: domain.Credentials{Username: "admin", Password: "x"}},
		clock:   &stepClock{t: autoRejectT0},
		devices: newDeviceRepo(),
		log:     &lines{},
	}
	f.uc = usecase.NewAutoReject(fakeFactory{gw: gw.fakeGateway, rp: gw}, newVault(), fakeDisco{}, f.devices,
		newMemAutoReject(), fixedSelf{phoneIP}, f.clock, f.log)
	return f
}

func (f *autoRejectFixture) enable(t *testing.T) {
	t.Helper()
	if _, err := f.uc.Enable(context.Background(), f.in); err != nil {
		t.Fatalf("Enable: %v", err)
	}
}

func (f *autoRejectFixture) run(t *testing.T) usecase.AutoRejectRun {
	t.Helper()
	res, err := f.uc.Enforce(context.Background(), f.in)
	if err != nil {
		t.Fatalf("Enforce: %v", err)
	}
	return res
}

func (f *autoRejectFixture) state(t *testing.T, mac string) domain.AutoRejectState {
	t.Helper()
	st, err := f.uc.Status(context.Background(), f.in.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range st.Devices {
		if d.MAC == domain.MustMAC(mac) {
			return d.State
		}
	}
	return ""
}

// --- tests ------------------------------------------------------------------

func TestAutoReject_RefusedAfter15MinutesNotBefore(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "Inconnu")
	f.run(t) // first seen now

	f.clock.advance(14 * time.Minute)
	if res := f.run(t); len(res.Refused) != 0 || f.gw.refusals(stranger) != 0 {
		t.Fatalf("refused at 14 min: %+v", res)
	}
	if got := f.state(t, stranger); got != domain.AutoRejectPending {
		t.Fatalf("state at 14 min = %q, want pending", got)
	}

	f.clock.advance(time.Minute)
	res := f.run(t)
	if len(res.Refused) != 1 || res.Refused[0].MAC != domain.MustMAC(stranger) || res.Refused[0].DisplayName() != "Inconnu" {
		t.Fatalf("at 15 min: refused = %+v, want the stranger", res.Refused)
	}
	if f.gw.refusals(stranger) != 1 {
		t.Fatalf("Wi-Fi refusals = %d, want 1", f.gw.refusals(stranger))
	}
	if got := f.state(t, stranger); got != domain.AutoRejectRefused {
		t.Fatalf("state = %q, want refused", got)
	}
	if f.log.count("auto : non accepté") != 1 {
		t.Fatalf("journal: %q", f.log.all)
	}
}

func TestAutoReject_NoDoubleRefusal(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	for _, step := range []time.Duration{15 * time.Minute, time.Minute, time.Hour} {
		f.clock.advance(step)
		f.run(t)
	}
	if n := f.gw.refusals(stranger); n != 1 {
		t.Fatalf("Wi-Fi refusals = %d, want exactly 1", n)
	}
	if f.log.count("auto : non accepté") != 1 {
		t.Fatalf("journal has %d refusals, want 1", f.log.count("auto : non accepté"))
	}
}

func TestAutoReject_ApprovedIsKept(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	f.clock.advance(5 * time.Minute)
	// "Accepter" on the new-device notification.
	if err := f.uc.Accept(context.Background(), f.in.BaseURL, domain.MustMAC(stranger)); err != nil {
		t.Fatal(err)
	}
	f.clock.advance(time.Hour)
	if res := f.run(t); len(res.Refused) != 0 || f.gw.refusals(stranger) != 0 {
		t.Fatalf("an approved device was refused: %+v", res)
	}
	if got := f.state(t, stranger); got != domain.AutoRejectAccepted {
		t.Fatalf("state = %q, want accepted", got)
	}
}

func TestAutoReject_ApprovedBeforeFirstSeenIsKept(t *testing.T) {
	// The notification can be approved before the rule's own tick saw it.
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	if err := f.uc.Accept(context.Background(), f.in.BaseURL, domain.MustMAC(stranger)); err != nil {
		t.Fatal(err)
	}
	f.run(t)
	f.clock.advance(time.Hour)
	if res := f.run(t); len(res.Refused) != 0 {
		t.Fatalf("refused: %+v", res)
	}
}

func TestAutoReject_RenamedIsKept(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	f.clock.advance(10 * time.Minute)
	// The user names it: the device list put it in the inventory first.
	mac := domain.MustMAC(stranger)
	routerID := usecase.RouterID(f.in.BaseURL)
	if _, err := f.devices.Upsert(context.Background(), domain.Device{RouterID: routerID, MAC: mac}); err != nil {
		t.Fatal(err)
	}
	if err := f.devices.SetCustomName(context.Background(), routerID, mac, "Tablette de Awa"); err != nil {
		t.Fatal(err)
	}
	f.clock.advance(10 * time.Minute)
	if res := f.run(t); len(res.Refused) != 0 || f.gw.refusals(stranger) != 0 {
		t.Fatalf("a renamed device was refused: %+v", res)
	}
	if got := f.state(t, stranger); got != domain.AutoRejectAccepted {
		t.Fatalf("state = %q, want accepted", got)
	}
}

func TestAutoReject_OwnPhoneNever(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	// The phone comes back with a new random MAC: a new device, but its IP
	// is the phone's own.
	f.gw.devices = f.gw.devices[:1]
	f.gw.join("cc:00:00:00:00:09", phoneIP, "")
	f.run(t)
	f.clock.advance(2 * time.Hour)
	if res := f.run(t); len(res.Refused) != 0 {
		t.Fatalf("the phone running the app was refused: %+v", res)
	}
	if f.gw.refusals("cc:00:00:00:00:09") != 0 {
		t.Fatal("the phone running the app was refused at the box")
	}
}

func TestAutoReject_PresentWhenEnabledAreAccepted(t *testing.T) {
	f := autoRejectSetup(t)
	st, err := f.uc.Enable(context.Background(), f.in)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Settings.Enabled || len(st.Devices) != 2 {
		t.Fatalf("status after Enable = %+v, want on with the 2 present devices", st)
	}
	for _, d := range st.Devices {
		if d.State != domain.AutoRejectAccepted {
			t.Fatalf("%s is %q, want accepted", d.MAC, d.State)
		}
	}
	f.clock.advance(time.Hour)
	if res := f.run(t); len(res.Refused) != 0 {
		t.Fatalf("a device present at activation was refused: %+v", res)
	}
}

func TestAutoReject_SettingOffDoesNothing(t *testing.T) {
	f := autoRejectSetup(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	f.clock.advance(time.Hour)
	if res := f.run(t); res.Enabled || len(res.Refused) != 0 || f.gw.refusals(stranger) != 0 {
		t.Fatalf("never turned on, yet: %+v", res)
	}

	// On, a stranger is seen, then off before its delay: nothing either.
	f.enable(t)
	f.gw.join(stranger2, "192.168.1.31", "")
	f.run(t)
	if err := f.uc.Disable(context.Background(), f.in.BaseURL); err != nil {
		t.Fatal(err)
	}
	f.clock.advance(time.Hour)
	if res := f.run(t); res.Enabled || len(res.Refused) != 0 || f.gw.refusals(stranger2) != 0 {
		t.Fatalf("turned off, yet: %+v", res)
	}
	st, _ := f.uc.Status(context.Background(), f.in.BaseURL)
	if st.Settings.Enabled || len(st.Devices) != 0 {
		t.Fatalf("status after Disable = %+v", st)
	}
}

func TestAutoReject_RestoredIsAcceptedForGood(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	f.clock.advance(15 * time.Minute)
	f.run(t)
	// "Rendre l'accès" (free) accepts it.
	if err := f.uc.Accept(context.Background(), f.in.BaseURL, domain.MustMAC(stranger)); err != nil {
		t.Fatal(err)
	}
	f.clock.advance(time.Hour)
	f.run(t)
	if n := f.gw.refusals(stranger); n != 1 {
		t.Fatalf("Wi-Fi refusals = %d, want 1 (none after the restore)", n)
	}
	if got := f.state(t, stranger); got != domain.AutoRejectAccepted {
		t.Fatalf("state = %q, want accepted", got)
	}
}

func TestAutoReject_FailedRefusalIsRetried(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	f.clock.advance(15 * time.Minute)
	f.gw.blockErr = errors.New("box busy")
	if res := f.run(t); len(res.Refused) != 0 {
		t.Fatalf("reported refused although the box refused the write: %+v", res)
	}
	if got := f.state(t, stranger); got != domain.AutoRejectPending {
		t.Fatalf("state = %q, want still pending", got)
	}
	f.gw.blockErr = nil
	if res := f.run(t); len(res.Refused) != 1 {
		t.Fatalf("not retried: %+v", res)
	}
}

// RFC-007 D5 amends RFC-006: an automatic refusal is a firewall rule
// (« Couper internet »), never a Wi-Fi refusal any more, so a box without a
// Wi-Fi access control runs the rule too.
func TestAutoReject_RefusesWithTheFirewall(t *testing.T) {
	f := autoRejectSetup(t)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	f.clock.advance(domain.AutoRejectDelay)
	if res := f.run(t); len(res.Refused) != 1 || f.gw.refusals(stranger) != 1 {
		t.Fatalf("refused = %+v, %d firewall rule(s); want the stranger", res.Refused, f.gw.refusals(stranger))
	}
	if n := len(f.gw.wifiRefused); n != 0 {
		t.Fatalf("%d Wi-Fi refusal(s), want none", n)
	}
	if f.log.count("internet coupé") != 1 {
		t.Errorf("journal: %q", f.log.all)
	}

	gw := &fakeGateway{meta: f660Meta()}
	in := usecase.MACFilterInput{BaseURL: "http://192.168.1.1", AdapterID: "zte_f660", Creds: domain.Credentials{Username: "admin", Password: "x"}}
	uc := usecase.NewAutoReject(fakeFactory{gw: gw}, newVault(), fakeDisco{}, newDeviceRepo(), newMemAutoReject(),
		fixedSelf{phoneIP}, &stepClock{t: autoRejectT0}, nopLogger{})
	if _, err := uc.Enable(context.Background(), in); err != nil {
		t.Fatalf("Enable on a box without Wi-Fi access control: %v", err)
	}
}

// memWiFiLock is an in-memory port.WiFiLockRepository.
type memWiFiLock struct {
	mu     sync.Mutex
	intent map[string]domain.WiFiLockIntent
	cut    map[string]bool // "<router>|<mac>"
}

func (m *memWiFiLock) WiFiLockIntent(_ context.Context, id string) (domain.WiFiLockIntent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.intent[id], nil
}
func (m *memWiFiLock) SaveWiFiLockIntent(_ context.Context, id string, i domain.WiFiLockIntent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.intent == nil {
		m.intent = map[string]domain.WiFiLockIntent{}
	}
	m.intent[id] = i
	return nil
}
func (m *memWiFiLock) WiFiCutMACs(_ context.Context, id string) ([]domain.MAC, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []domain.MAC
	for k, on := range m.cut {
		if on && strings.HasPrefix(k, id+"|") {
			out = append(out, domain.MustMAC(strings.TrimPrefix(k, id+"|")))
		}
	}
	return out, nil
}
func (m *memWiFiLock) SetWiFiCut(_ context.Context, id string, mac domain.MAC, cut bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cut == nil {
		m.cut = map[string]bool{}
	}
	m.cut[id+"|"+mac.String()] = cut
	return nil
}

// RFC-007 D5: while the Wi-Fi is closed auto-reject has nothing left to do;
// it pauses, and takes up again once the Wi-Fi is open.
func TestAutoReject_PausedWhileTheWiFiIsClosed(t *testing.T) {
	f := autoRejectSetup(t)
	lock := &memWiFiLock{}
	f.uc.WithWiFiLock(lock)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.run(t)
	routerID := usecase.RouterID(f.in.BaseURL)
	_ = lock.SaveWiFiLockIntent(context.Background(), routerID, domain.WiFiLockIntent{Closed: true})
	f.clock.advance(time.Hour)
	f.gw.loggedIn = false
	res := f.run(t)
	if !res.Enabled || !res.Paused || len(res.Refused) != 0 || f.gw.refusals(stranger) != 0 {
		t.Fatalf("closed: %+v, %d refusal(s); want paused", res, f.gw.refusals(stranger))
	}
	if f.gw.loggedIn {
		t.Error("a paused round reached the box")
	}
	_ = lock.SaveWiFiLockIntent(context.Background(), routerID, domain.WiFiLockIntent{})
	if res := f.run(t); res.Paused || len(res.Refused) != 1 || f.gw.refusals(stranger) != 1 {
		t.Fatalf("open again: %+v, want the stranger refused", res)
	}
}

// --- the relay's side (#36) -------------------------------------------------

func (f *autoRejectFixture) adopt(t *testing.T, p usecase.AutoRejectPolicy) {
	t.Helper()
	if err := f.uc.Adopt(context.Background(), f.in.BaseURL, p); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
}

// A relay takes the rule as the owner's phone set it: the phone's accepted
// devices, not whatever happens to be on the box when the relay starts. A
// stranger that was already pending on the phone is not accepted by the
// relay just because it was present.
func TestAutoReject_AdoptTakesThePhonesAcceptedList(t *testing.T) {
	f := autoRejectSetup(t)
	f.gw.join(stranger, "192.168.1.30", "Inconnu")
	since := autoRejectT0.Add(-time.Hour)
	f.adopt(t, usecase.AutoRejectPolicy{Enabled: true, Since: since, Accepted: []domain.MAC{domain.MustMAC(presentA)}})
	if f.gw.loggedIn {
		t.Fatal("adopting the rule logged in to the box")
	}
	st, err := f.uc.Status(context.Background(), f.in.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Settings.Enabled || !st.Settings.Since.Equal(since) {
		t.Fatalf("settings = %+v, want on since %s", st.Settings, since)
	}
	if got := f.state(t, presentA); got != domain.AutoRejectAccepted {
		t.Fatalf("accepted device is %q", got)
	}

	f.run(t) // the stranger is first seen by the relay now
	f.clock.advance(domain.AutoRejectDelay)
	res := f.run(t)
	if len(res.Refused) != 1 || res.Refused[0].MAC != domain.MustMAC(stranger) {
		t.Fatalf("refused = %+v, want the stranger", res.Refused)
	}
	if f.gw.refusals(presentA) != 0 {
		t.Fatal("an accepted device was refused")
	}
}

// A new version of the same rule only adds what the phone accepted since;
// the relay's own pending clock and refusals survive it.
func TestAutoReject_AdoptAgainKeepsTheRelaysProgress(t *testing.T) {
	f := autoRejectSetup(t)
	since := autoRejectT0
	f.adopt(t, usecase.AutoRejectPolicy{Enabled: true, Since: since, Accepted: []domain.MAC{domain.MustMAC(presentA)}})
	f.gw.join(stranger, "192.168.1.30", "")
	f.gw.join(stranger2, "192.168.1.31", "")
	f.run(t)
	f.clock.advance(10 * time.Minute)

	// The household names stranger2 on the phone: the owner's next policy
	// lists it as accepted.
	f.adopt(t, usecase.AutoRejectPolicy{Enabled: true, Since: since,
		Accepted: []domain.MAC{domain.MustMAC(presentA), domain.MustMAC(stranger2)}})
	if got := f.state(t, stranger); got != domain.AutoRejectPending {
		t.Fatalf("stranger = %q, want still pending", got)
	}
	f.clock.advance(5 * time.Minute)
	res := f.run(t)
	if len(res.Refused) != 1 || res.Refused[0].MAC != domain.MustMAC(stranger) {
		t.Fatalf("refused = %+v, want the stranger only, on its first clock", res.Refused)
	}
	if f.gw.refusals(stranger2) != 0 {
		t.Fatal("a device accepted on the phone was refused by the relay")
	}
}

// Turning the rule off on the phone turns it off on the relay.
func TestAutoReject_AdoptOff(t *testing.T) {
	f := autoRejectSetup(t)
	f.adopt(t, usecase.AutoRejectPolicy{Enabled: true, Since: autoRejectT0})
	f.adopt(t, usecase.AutoRejectPolicy{})
	if res := f.run(t); res.Enabled {
		t.Fatalf("still on after the phone turned it off: %+v", res)
	}
}

// The phone stands down while the relay's last hub heartbeat is fresh.
func TestAutoReject_RelayEnforcesOnTheRulesClock(t *testing.T) {
	f := autoRejectSetup(t)
	last := f.clock.Now().Add(-90 * time.Second)
	if !f.uc.RelayEnforces(last) {
		t.Fatal("a relay heard from 90 s ago does not enforce")
	}
	f.clock.advance(time.Minute)
	if f.uc.RelayEnforces(last) {
		t.Fatal("a relay silent for 2 min 30 still enforces")
	}
	if f.uc.RelayEnforces(time.Time{}) {
		t.Fatal("no relay at all enforces")
	}
}

// fakeApprovals is the household's approvals (#58) on one box.
type fakeApprovals struct {
	mu   sync.Mutex
	macs map[string][]domain.MAC
	err  error
}

func (a *fakeApprovals) ApprovedMACs(_ context.Context, routerID string) ([]domain.MAC, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]domain.MAC(nil), a.macs[routerID]...), a.err
}

// RFC-007 D1: « Autorisé » is one piece of data, the approval. A device the
// household approved is accepted by auto-reject without a separate accept.
func TestAutoReject_FedFromTheApprovals(t *testing.T) {
	f := autoRejectSetup(t)
	ap := &fakeApprovals{macs: map[string][]domain.MAC{}}
	f.uc.WithApprovals(ap)
	f.enable(t)
	f.gw.join(stranger, "192.168.1.30", "")
	f.gw.join(stranger2, "192.168.1.31", "")
	f.run(t)
	f.clock.advance(5 * time.Minute)
	routerID := usecase.RouterID(f.in.BaseURL)
	ap.macs[routerID] = []domain.MAC{domain.MustMAC(stranger)}
	f.clock.advance(time.Hour)
	res := f.run(t)
	if f.gw.refusals(stranger) != 0 {
		t.Fatalf("an approved device was refused: %+v", res)
	}
	if got := f.state(t, stranger); got != domain.AutoRejectAccepted {
		t.Fatalf("approved device state = %q, want accepted", got)
	}
	if f.gw.refusals(stranger2) != 1 {
		t.Fatalf("the device nobody approved: %d refusal(s), want 1", f.gw.refusals(stranger2))
	}

	// Approvals another box holds accept nothing here; approvals that cannot
	// be read leave the rule running on what it tracked.
	g := autoRejectSetup(t)
	ap2 := &fakeApprovals{macs: map[string][]domain.MAC{"another-box": {domain.MustMAC(stranger)}}}
	g.uc.WithApprovals(ap2)
	g.enable(t)
	g.gw.join(stranger, "192.168.1.30", "")
	g.run(t)
	ap2.err = errors.New("approvals unreadable")
	g.clock.advance(15 * time.Minute)
	g.run(t)
	if g.gw.refusals(stranger) != 1 {
		t.Fatalf("approved on another box only: %d refusal(s), want 1", g.gw.refusals(stranger))
	}
}
