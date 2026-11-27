package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// WiFiLock is the closed Wi-Fi (RFC-007 D2, D4, D5): « Fermer mon Wi-Fi »,
// « Ouvrir mon Wi-Fi », the add window (« Ajouter un nouvel appareil »,
// « Encore 5 min », « Refermer maintenant ») and what Autoriser, À vérifier,
// Bloquer and Débloquer write on the box, open or closed.
//
// It owns the guarantees RFC-007 D7 gives the use case: this phone, the
// relays and the Owner phones are always in the allow list; the places are
// checked before any write; the add window's deadline is stored, and the
// next one to reach the box after it closes the Wi-Fi again; a box found
// open without a window is never closed by kergui. The adapter
// (port.WiFiAllowList) owns the box's ordering and the rollback.
//
// The approvals themselves (« Autorisé ») are kept by the host (#58): SetTrust
// writes the box side of a change and the « déconnecté du Wi-Fi » flag, and
// reads the approvals to build the allow list.
type WiFiLock struct {
	factory   port.RouterFactory
	vault     port.CredentialVault
	disco     port.DiscoveryPort
	store     port.WiFiLockRepository
	clock     port.Clock
	log       port.Logger
	self      port.SelfLocator      // nil: no device to spare
	approvals port.ApprovalReader   // nil: none
	household port.HouseholdDevices // nil: no relay, no Owner phone
	purge     *PurgeStaleRules      // nil: a full list is reported as is
	spawn     func(func())

	mu        sync.Mutex
	restoring map[string]bool // boxes whose refusals are being written back
	foundOpen map[string]bool // boxes found open without a window, journaled
}

// The state the Wi-Fi button shows (RFC-007 D12), from what the box says.
const (
	WiFiStateOpen   = "open"   // « Wi-Fi ouvert »
	WiFiStateClosed = "closed" // « Wi-Fi fermé »
	WiFiStateWindow = "window" // « Wi-Fi ouvert pour N min »
	WiFiStateBusy   = "busy"   // « … En cours »
)

// NewWiFiLock wires the interactor.
func NewWiFiLock(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, store port.WiFiLockRepository,
	c port.Clock, l port.Logger) *WiFiLock {
	return &WiFiLock{factory: f, vault: v, disco: d, store: store, clock: c, log: l,
		spawn:     func(f func()) { go f() },
		restoring: map[string]bool{}, foundOpen: map[string]bool{}}
}

// WithSelf finds the device running Kergui at the address self gives: it is
// always in the allow list and never cut (selfguard.go). It returns uc.
func (uc *WiFiLock) WithSelf(self port.SelfLocator) *WiFiLock { uc.self = self; return uc }

// WithApprovals reads « Autorisé » from a (RFC-007 D1). It returns uc.
func (uc *WiFiLock) WithApprovals(a port.ApprovalReader) *WiFiLock { uc.approvals = a; return uc }

// WithHousehold names the relays and the Owner phones, always in the allow
// list. It returns uc.
func (uc *WiFiLock) WithHousehold(h port.HouseholdDevices) *WiFiLock { uc.household = h; return uc }

// WithPurge makes a block or a Wi-Fi refusal the box refuses because its
// list is full first purge the stale rules, then try once more (RFC-007 D6).
// It returns uc.
func (uc *WiFiLock) WithPurge(p *PurgeStaleRules) *WiFiLock { uc.purge = p; return uc }

// WithBackground runs the work Open leaves for later (the refusals written
// back) with run instead of a goroutine. It returns uc.
func (uc *WiFiLock) WithBackground(run func(func())) *WiFiLock { uc.spawn = run; return uc }

// lockSession is one logged-in look at the box: the gateway, the box's Wi-Fi
// access control, and the stored intent.
type lockSession struct {
	gw       port.RouterPort
	al       port.WiFiAllowList
	box      domain.WiFiLockBox
	intent   domain.WiFiLockIntent
	routerID string
	baseURL  string
}

func (uc *WiFiLock) session(ctx context.Context, in MACFilterInput) (*lockSession, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	al, ok := gw.(port.WiFiAllowList)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s cannot close the Wi-Fi", domain.ErrNotImplemented, gw.Meta().ID)
	}
	s := &lockSession{gw: gw, al: al, routerID: RouterID(in.BaseURL), baseURL: in.BaseURL}
	if s.box, err = al.WiFiLockBox(ctx); err != nil {
		return nil, err
	}
	if s.intent, err = uc.store.WiFiLockIntent(ctx, s.routerID); err != nil {
		return nil, err
	}
	return s, nil
}

// reread reads the box again after a write.
func (s *lockSession) reread(ctx context.Context) error {
	box, err := s.al.WiFiLockBox(ctx)
	if err == nil {
		s.box = box
	}
	return err
}

// lockAPs splits the enabled access points: the visitors' ones (the
// household's answer, D2, or the box's own mark), the household's (every
// other), and the ones a close shuts.
func lockAPs(box domain.WiFiLockBox, visitors []string, visitorsClosed bool) (household, guests, closed []string) {
	for _, ap := range box.APs {
		if ap.Visitors || slices.Contains(visitors, ap.ID) {
			guests = append(guests, ap.ID)
		} else {
			household = append(household, ap.ID)
		}
	}
	closed = append([]string(nil), household...)
	if visitorsClosed {
		closed = append(closed, guests...)
	}
	return household, guests, closed
}

// closedAPs are the access points the stored intent shuts, as the box has
// them; from the box when the household never closed (D2).
func (s *lockSession) closedAPs() []string {
	want := s.intent.ClosedAPs()
	if len(s.intent.HouseholdAPs) == 0 {
		_, _, want = lockAPs(s.box, s.intent.VisitorAPs, s.intent.VisitorsClosed)
	}
	out := make([]string, 0, len(want))
	for _, id := range want {
		if _, ok := s.box.AP(id); ok {
			out = append(out, id)
		}
	}
	return out
}

// householdAPs are the household's access points, as the box has them.
func (s *lockSession) householdAPs() []string {
	want := s.intent.HouseholdAPs
	if len(want) == 0 {
		want, _, _ = lockAPs(s.box, s.intent.VisitorAPs, false)
	}
	out := make([]string, 0, len(want))
	for _, id := range want {
		if _, ok := s.box.AP(id); ok {
			out = append(out, id)
		}
	}
	return out
}

// inMode reports whether every one of aps is in mode (and there is one).
func (s *lockSession) inMode(aps []string, mode string) bool {
	for _, id := range aps {
		if ap, ok := s.box.AP(id); !ok || ap.Mode != mode {
			return false
		}
	}
	return len(aps) > 0
}

// boxClosed reports whether the box is closed as the household asked.
func (s *lockSession) boxClosed() bool { return s.inMode(s.closedAPs(), domain.WiFiModeAllow) }

// names are the broadcast names of aps, for the journal.
func (s *lockSession) names(aps []string) string {
	seen := map[string]bool{}
	var out []string
	for _, id := range aps {
		ap, _ := s.box.AP(id)
		n := apLabel(ap)
		if ap.ID == "" {
			n = id
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return strings.Join(out, ", ")
}

// members are who the allow list is built from: this phone, the relays and
// the Owner phones (always in it), the approvals, and the blocked devices
// (firewall rules and « déconnectés du Wi-Fi », never in it).
type members struct {
	self     domain.MAC
	relays   []domain.MAC
	owners   []domain.MAC
	always   macSet
	approved macSet
	blocked  macSet
	cut      macSet
	firewall macSet
}

func (uc *WiFiLock) members(ctx context.Context, s *lockSession) (members, error) {
	var m members
	me, ok, err := selfDevice(ctx, uc.self, s.gw, s.baseURL)
	if err != nil {
		return m, err
	}
	if ok {
		m.self = me.MAC
	}
	m.always = newMACSet([]domain.MAC{m.self})
	if uc.household != nil {
		relays, err := uc.household.RelayMACs(ctx, s.routerID)
		if err != nil {
			return m, fmt.Errorf("the household's relays: %w", err)
		}
		owners, err := uc.household.OwnerMACs(ctx, s.routerID)
		if err != nil {
			return m, fmt.Errorf("the Owner phones: %w", err)
		}
		m.relays, m.owners = relays, owners
		for _, l := range [][]domain.MAC{relays, owners} {
			for _, x := range l {
				m.always.add(x)
			}
		}
	}
	m.approved = macSet{}
	if uc.approvals != nil {
		approved, err := uc.approvals.ApprovedMACs(ctx, s.routerID)
		if err != nil {
			return m, fmt.Errorf("the approvals: %w", err)
		}
		m.approved = newMACSet(approved)
	}
	rules, err := s.gw.AccessRules(ctx)
	if err != nil && !errors.Is(err, domain.ErrNotImplemented) {
		return m, err
	}
	m.firewall = macSet{}
	for _, r := range rules {
		if r.Mode == domain.AccessBlock {
			m.firewall.add(r.MAC)
		}
	}
	cut, err := uc.store.WiFiCutMACs(ctx, s.routerID)
	if err != nil {
		return m, err
	}
	m.cut = newMACSet(cut)
	m.blocked = newMACSet(m.firewall.list(), m.cut.list())
	return m, nil
}

// household is the domain's view of m.
func (m members) household() domain.WiFiHousehold {
	return domain.WiFiHousehold{Self: m.self, Relays: m.relays, Owners: m.owners,
		Approved: m.approved.list(), Blocked: m.blocked.list()}
}

// --- closing ----------------------------------------------------------------

// WiFiCloseInput is « Fermer mon Wi-Fi ».
type WiFiCloseInput struct {
	MACFilterInput
	// VisitorAPs are the networks the household says are for its visitors
	// (« Ce réseau est-il pour vos visiteurs ? », D2); nil keeps the answer
	// stored.
	VisitorAPs []string
	// VisitorsClosed: « Fermer aussi le Wi-Fi des visiteurs ».
	VisitorsClosed bool
}

// WiFiLockPreview is what a close would write, read before any write
// (RFC-007 D4 step 1).
type WiFiLockPreview struct {
	HouseholdAPs, VisitorAPs, ClosedAPs []string
	VisitorsClosed                      bool
	// Devices is the allow list; Always are the ones in it that cannot be
	// unchecked: this phone, the relays, the Owner phones.
	Devices, Always []domain.MAC
	// Self is this phone, zero when it is not on the box (through the
	// relay). SelfPrivate: its address may change (D10, the advice line).
	Self        domain.MAC
	SelfPrivate bool
	// Places is how many devices the list can hold (D3), PlacesKnown false
	// when the model never said its limit.
	Places      int
	PlacesKnown bool
	// Full is « liste pleine », with the numbers, nil when it fits.
	Full *domain.AccessListFullError
}

// Preview says what « Fermer mon Wi-Fi » would write. It writes nothing.
func (uc *WiFiLock) Preview(ctx context.Context, in WiFiCloseInput) (WiFiLockPreview, error) {
	s, err := uc.session(ctx, in.MACFilterInput)
	if err != nil {
		return WiFiLockPreview{}, err
	}
	p, _, _, err := uc.preview(ctx, s, in)
	return p, err
}

func (uc *WiFiLock) preview(ctx context.Context, s *lockSession, in WiFiCloseInput) (WiFiLockPreview, domain.WiFiLockTarget, members, error) {
	visitors := in.VisitorAPs
	if visitors == nil {
		visitors = s.intent.VisitorAPs
	}
	household, guests, closed := lockAPs(s.box, visitors, in.VisitorsClosed)
	p := WiFiLockPreview{HouseholdAPs: household, VisitorAPs: guests, ClosedAPs: closed, VisitorsClosed: in.VisitorsClosed}
	if len(closed) == 0 {
		return p, domain.WiFiLockTarget{}, members{}, fmt.Errorf("%w: aucun réseau Wi-Fi de la maison à fermer", domain.ErrInvalidInput)
	}
	m, err := uc.members(ctx, s)
	if err != nil {
		return p, domain.WiFiLockTarget{}, m, err
	}
	target, err := domain.HouseholdAllowList(closed, domain.WiFiModeAllow, m.household())
	if err != nil {
		return p, target, m, err
	}
	p.Devices, p.Always = target.MACs, m.always.list()
	p.Self, p.SelfPrivate = m.self, !m.self.IsZero() && m.self.IsRandomized()
	p.Places, p.PlacesKnown = s.box.Places(closed)
	var full *domain.AccessListFullError
	if errors.As(s.box.CheckFits(target), &full) {
		p.Full = full
	}
	return p, target, m, nil
}

// Close is « Fermer mon Wi-Fi » (RFC-007 D4): the preview, « liste pleine »
// before any write, the Wi-Fi refusals of the household's networks kept in
// kergui, then the box converged to Allow. A close the box refuses is undone
// by the adapter, and nothing is recorded as closed.
func (uc *WiFiLock) Close(ctx context.Context, in WiFiCloseInput) (WiFiLockPreview, error) {
	s, err := uc.session(ctx, in.MACFilterInput)
	if err != nil {
		return WiFiLockPreview{}, err
	}
	p, target, m, err := uc.preview(ctx, s, in)
	if err != nil {
		return p, err
	}
	if p.Full != nil {
		uc.log.Warnf("Wi-Fi pas fermé : la liste Wi-Fi est pleine (%d appareils pour %d places)", p.Full.Used, p.Full.Limit)
		return p, p.Full
	}
	// D4 step 3: the refusals leave the box and stay recorded in kergui,
	// to be written back at the next open.
	kept := 0
	for _, mac := range banRefusals(s.box, p.ClosedAPs) {
		if m.always.has(mac) || m.cut.has(mac) {
			continue
		}
		if err := uc.store.SetWiFiCut(ctx, s.routerID, mac, true); err != nil {
			return p, err
		}
		kept++
	}
	if kept > 0 {
		uc.log.Infof("%d refus Wi-Fi gardé(s) dans Kergui, rétabli(s) à la prochaine ouverture", kept)
	}
	if err := s.al.ApplyWiFiLock(ctx, target); err != nil {
		uc.log.Warnf("Fermeture du Wi-Fi annulée, la box est remise comme avant : %v", err)
		return p, err
	}
	intent := domain.WiFiLockIntent{
		Closed: true, HouseholdAPs: p.HouseholdAPs, VisitorAPs: p.VisitorAPs, VisitorsClosed: p.VisitorsClosed,
		Since: uc.clock.Now(),
	}
	if err := uc.store.SaveWiFiLockIntent(ctx, s.routerID, intent); err != nil {
		return p, err
	}
	uc.setFoundOpen(s.routerID, false)
	uc.log.Infof("Wi-Fi fermé : %d appareil(s) autorisé(s) sur %s ; les autres en sont retirés dans moins d'une minute",
		len(target.MACs), s.names(p.ClosedAPs))
	return p, nil
}

// --- opening ----------------------------------------------------------------

// WiFiOpenResult is what « Ouvrir mon Wi-Fi » did.
type WiFiOpenResult struct {
	// Restoring are the devices « déconnectés du Wi-Fi » whose refusals are
	// being written back in the background (« … En cours »).
	Restoring []domain.MAC
}

// Open is « Ouvrir mon Wi-Fi », free (RFC-007 D4, D8): the household's
// networks go to Disabled at once, the allow list kept; the refusals of the
// devices « déconnectés du Wi-Fi » are written back afterwards, in the
// background (RestoreWiFiRefusals).
func (uc *WiFiLock) Open(ctx context.Context, in MACFilterInput) (WiFiOpenResult, error) {
	s, err := uc.session(ctx, in)
	if err != nil {
		return WiFiOpenResult{}, err
	}
	var toOpen []string
	for _, ap := range s.box.APs {
		if ap.Mode == domain.WiFiModeAllow {
			toOpen = append(toOpen, ap.ID)
		}
	}
	if len(toOpen) > 0 {
		if err := s.al.SetWiFiMode(ctx, toOpen, domain.WiFiModeDisabled); err != nil {
			uc.log.Warnf("Ouverture du Wi-Fi impossible : %v", err)
			return WiFiOpenResult{}, err
		}
	}
	intent := s.intent.EndWindow()
	intent.Closed, intent.Since = false, uc.clock.Now()
	if err := uc.store.SaveWiFiLockIntent(ctx, s.routerID, intent); err != nil {
		return WiFiOpenResult{}, err
	}
	s.intent = intent
	uc.setFoundOpen(s.routerID, false)
	uc.log.Infof("Wi-Fi ouvert : tout appareil qui a le mot de passe peut se connecter")

	m, err := uc.members(ctx, s)
	if err != nil {
		uc.log.Warnf("Refus Wi-Fi pas rétablis : %v", err)
		return WiFiOpenResult{}, nil
	}
	var res WiFiOpenResult
	for _, mac := range m.cut.list() {
		if !m.always.has(mac) {
			res.Restoring = append(res.Restoring, mac)
		}
	}
	if len(res.Restoring) == 0 {
		return res, nil
	}
	uc.setRestoring(s.routerID, true)
	bg := context.WithoutCancel(ctx)
	uc.spawn(func() {
		defer uc.setRestoring(s.routerID, false)
		if err := uc.RestoreWiFiRefusals(bg, in); err != nil {
			uc.log.Warnf("Refus Wi-Fi pas rétablis : %v", err)
		}
	})
	return res, nil
}

// RestoreWiFiRefusals is Open's second step (RFC-007 D4): on the household's
// networks, the allow rules go, the refusals of the devices « déconnectés du
// Wi-Fi » are written, and the networks go to Ban. Meanwhile those devices
// have no internet (their firewall rule). It does nothing once the Wi-Fi is
// closed again.
func (uc *WiFiLock) RestoreWiFiRefusals(ctx context.Context, in MACFilterInput) error {
	s, err := uc.session(ctx, in)
	if err != nil {
		return err
	}
	if s.intent.Closed {
		return nil
	}
	m, err := uc.members(ctx, s)
	if err != nil {
		return err
	}
	if len(m.cut) == 0 {
		return nil
	}
	if err := uc.writeRefusals(ctx, s, m, domain.MAC{}, domain.MAC{}); err != nil {
		return err
	}
	uc.log.Infof("Refus Wi-Fi rétablis pour %d appareil(s) déconnecté(s) du Wi-Fi", len(m.cut))
	return nil
}

// writeRefusals converges the household's networks of an open Wi-Fi to the
// devices « déconnectés du Wi-Fi » plus add, minus drop, plus the refusals
// the box already holds there (D5), through ApplyWiFiLock in Ban. A full
// table purges the stale refusals and tries again; the devices whose
// refusals the purge removed are no longer « déconnectés du Wi-Fi ».
func (uc *WiFiLock) writeRefusals(ctx context.Context, s *lockSession, m members, add, drop domain.MAC) error {
	aps := s.householdAPs()
	spare := m.always.list()
	deny := func(box domain.WiFiLockBox) []domain.MAC {
		cut, err := uc.store.WiFiCutMACs(ctx, s.routerID)
		if err != nil {
			cut = m.cut.list()
		}
		set := newMACSet(cut, banRefusals(box, aps), []domain.MAC{add})
		set.drop(drop)
		return set.list()
	}
	forgotten := func(macs []domain.MAC) {
		for _, mac := range macs {
			if err := uc.store.SetWiFiCut(ctx, s.routerID, mac, false); err != nil {
				uc.log.Warnf("« déconnecté du Wi-Fi » de %s pas oublié : %v", mac, err)
			}
		}
	}
	mac := add
	if mac.IsZero() {
		mac = drop
	}
	return refuseWithPurge(ctx, s.gw, s.baseURL, mac, uc.purge, uc.log, forgotten, func() error {
		return applyWiFiRefusals(ctx, s.al, aps, deny, spare)
	})
}

// --- the add window ---------------------------------------------------------

// WiFiWindow is the add window as the panel shows it.
type WiFiWindow struct {
	// Until is the deadline, zero when no window runs.
	Until time.Time
	// MinutesLeft, rounded up (« Wi-Fi ouvert pour 3 min »).
	MinutesLeft int
	// CanExtend: « Encore 5 min » adds something.
	CanExtend bool
}

func (uc *WiFiLock) window(i domain.WiFiLockIntent) WiFiWindow {
	now := uc.clock.Now()
	if !i.WindowOpen(now) {
		return WiFiWindow{}
	}
	return WiFiWindow{Until: i.WindowUntil, MinutesLeft: i.MinutesLeft(now), CanExtend: i.CanExtend(now)}
}

// OpenWindow is « Ajouter un nouvel appareil (5 min) », Wi-Fi closed
// (RFC-007 D4): the closed networks go to Disabled, the allow rules kept, and
// the deadline is stored. A window already running is left as it is.
func (uc *WiFiLock) OpenWindow(ctx context.Context, in MACFilterInput) (WiFiWindow, error) {
	s, err := uc.session(ctx, in)
	if err != nil {
		return WiFiWindow{}, err
	}
	now := uc.clock.Now()
	if s.intent.WindowOpen(now) {
		return uc.window(s.intent), nil
	}
	intent, err := s.intent.StartWindow(now)
	if err != nil {
		return WiFiWindow{}, err
	}
	aps := s.closedAPs()
	if err := s.al.SetWiFiMode(ctx, aps, domain.WiFiModeDisabled); err != nil {
		uc.log.Warnf("Fenêtre d'ajout pas ouverte : %v", err)
		return WiFiWindow{}, err
	}
	if err := uc.store.SaveWiFiLockIntent(ctx, s.routerID, intent); err != nil {
		return WiFiWindow{}, err
	}
	uc.log.Infof("Fenêtre d'ajout ouverte pour %d min sur %s : un nouvel appareil peut se connecter",
		int(domain.WiFiWindowStep/time.Minute), s.names(aps))
	return uc.window(intent), nil
}

// ExtendWindow is « Encore 5 min »: the deadline moves, never past 15
// minutes from the window's start. The box is not reached: it stays open.
func (uc *WiFiLock) ExtendWindow(ctx context.Context, in MACFilterInput) (WiFiWindow, error) {
	routerID := RouterID(in.BaseURL)
	cur, err := uc.store.WiFiLockIntent(ctx, routerID)
	if err != nil {
		return WiFiWindow{}, err
	}
	intent, err := cur.ExtendWindow(uc.clock.Now())
	if err != nil {
		return uc.window(cur), err
	}
	if err := uc.store.SaveWiFiLockIntent(ctx, routerID, intent); err != nil {
		return WiFiWindow{}, err
	}
	w := uc.window(intent)
	uc.log.Infof("Fenêtre d'ajout prolongée : encore %d min", w.MinutesLeft)
	return w, nil
}

// CloseWindow is « Refermer maintenant »: the closed networks go back to
// Allow and the window is forgotten.
func (uc *WiFiLock) CloseWindow(ctx context.Context, in MACFilterInput) error {
	s, err := uc.session(ctx, in)
	if err != nil {
		return err
	}
	if !s.intent.Closed || s.intent.WindowUntil.IsZero() {
		return domain.ErrNoAddWindow
	}
	return uc.reclose(ctx, s, "Fenêtre d'ajout arrêtée : Wi-Fi refermé")
}

// reclose ends the add window: a mode flip back to Allow, the rules kept.
func (uc *WiFiLock) reclose(ctx context.Context, s *lockSession, line string) error {
	aps := s.closedAPs()
	if err := s.al.SetWiFiMode(ctx, aps, domain.WiFiModeAllow); err != nil {
		uc.log.Warnf("Le Wi-Fi n'a pas pu être refermé : %v", err)
		return err
	}
	intent := s.intent.EndWindow()
	if err := uc.store.SaveWiFiLockIntent(ctx, s.routerID, intent); err != nil {
		return err
	}
	s.intent = intent
	uc.log.Infof("%s sur %s", line, s.names(aps))
	return nil
}

// WiFiTick is what one round of the watch did.
type WiFiTick struct {
	// Reclosed: an add window was over, the Wi-Fi is closed again.
	Reclosed bool
	// FoundOpen: closed by the household, found open without a window.
	FoundOpen bool
}

// Tick is one round of whoever polls the box (the relay, the phone's watch
// service, the app back in the foreground; RFC-007 D4). An add window past
// its deadline is closed again; a box the household closed and that is found
// open without a window is written in the journal once, and never closed by
// kergui. An open Wi-Fi does not reach the box.
func (uc *WiFiLock) Tick(ctx context.Context, in MACFilterInput) (WiFiTick, error) {
	intent, err := uc.store.WiFiLockIntent(ctx, RouterID(in.BaseURL))
	if err != nil || !intent.Closed || intent.WindowOpen(uc.clock.Now()) {
		return WiFiTick{}, err
	}
	s, err := uc.session(ctx, in)
	if err != nil {
		return WiFiTick{}, err
	}
	if s.intent.WindowDue(uc.clock.Now()) {
		if err := uc.reclose(ctx, s, "Fenêtre d'ajout terminée : Wi-Fi refermé"); err != nil {
			return WiFiTick{}, err
		}
		return WiFiTick{Reclosed: true}, nil
	}
	if s.boxClosed() {
		uc.setFoundOpen(s.routerID, false)
		return WiFiTick{}, nil
	}
	uc.noteFoundOpen(s)
	return WiFiTick{FoundOpen: true}, nil
}

func (uc *WiFiLock) noteFoundOpen(s *lockSession) {
	uc.mu.Lock()
	seen := uc.foundOpen[s.routerID]
	uc.foundOpen[s.routerID] = true
	uc.mu.Unlock()
	if !seen {
		uc.log.Warnf("Wi-Fi trouvé ouvert sans fenêtre d'ajout (box remise à zéro, ou changée depuis sa page) : Kergui ne le referme pas de lui-même")
	}
}

func (uc *WiFiLock) setFoundOpen(routerID string, v bool) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	if v {
		uc.foundOpen[routerID] = true
	} else {
		delete(uc.foundOpen, routerID)
	}
}

func (uc *WiFiLock) setRestoring(routerID string, v bool) {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	if v {
		uc.restoring[routerID] = true
	} else {
		delete(uc.restoring, routerID)
	}
}

func (uc *WiFiLock) isRestoring(routerID string) bool {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	return uc.restoring[routerID]
}

// --- status -----------------------------------------------------------------

// WiFiLockStatus is what the button and the « Autorisés » tab show; the box
// is the truth for display (RFC-007 D4).
type WiFiLockStatus struct {
	State string
	// APs are the enabled networks as the box says.
	APs                      []domain.WiFiLockAP
	HouseholdAPs, VisitorAPs []string
	VisitorsClosed           bool
	Window                   WiFiWindow
	// Allowed is how many devices the closed networks list; Places as in
	// the preview.
	Allowed     int
	Places      int
	PlacesKnown bool
	// FoundOpen: the household closed it, the box is open with no window.
	FoundOpen bool
}

// Status reads the box and the stored intent. It writes nothing.
func (uc *WiFiLock) Status(ctx context.Context, in MACFilterInput) (WiFiLockStatus, error) {
	s, err := uc.session(ctx, in)
	if err != nil {
		return WiFiLockStatus{}, err
	}
	closed := s.closedAPs()
	st := WiFiLockStatus{
		APs: s.box.APs, HouseholdAPs: s.householdAPs(), VisitorAPs: s.intent.VisitorAPs,
		VisitorsClosed: s.intent.VisitorsClosed, Window: uc.window(s.intent),
	}
	st.Places, st.PlacesKnown = s.box.Places(closed)
	listed := macSet{}
	for _, id := range closed {
		for _, m := range s.box.Rules[id] {
			listed.add(m)
		}
	}
	st.Allowed = len(listed)
	now := uc.clock.Now()
	switch {
	case uc.isRestoring(s.routerID):
		st.State = WiFiStateBusy
	case s.intent.Closed && s.intent.WindowOpen(now):
		st.State = WiFiStateWindow
	case s.boxClosed():
		st.State = WiFiStateClosed
	default:
		st.State = WiFiStateOpen
		st.FoundOpen = s.intent.Closed && !s.intent.WindowDue(now)
		if st.FoundOpen {
			uc.noteFoundOpen(s)
		}
	}
	return st, nil
}

// --- D5: Autoriser, À vérifier, Bloquer, Débloquer --------------------------

// WiFiTrustInput is one device's new state (RFC-007 D1, D5). Débloquer is
// To: domain.TrustToCheck.
type WiFiTrustInput struct {
	MACFilterInput
	MAC domain.MAC
	To  domain.DeviceTrust
	// CutWiFi, with TrustBlocked: « Le déconnecter aussi du Wi-Fi ».
	CutWiFi bool
}

// lockMode is how the Wi-Fi is for D5: closed (the box in Allow as asked),
// in an add window (one past its deadline and not closed again yet
// included: the re-close keeps the rules), or open (found open included:
// the box is the truth).
func (uc *WiFiLock) lockMode(s *lockSession) string {
	switch {
	case !s.intent.Closed:
		return WiFiStateOpen
	case !s.intent.WindowUntil.IsZero() && s.inMode(s.closedAPs(), domain.WiFiModeDisabled):
		return WiFiStateWindow
	case s.boxClosed():
		return WiFiStateClosed
	}
	return WiFiStateOpen
}

// SetTrust writes the box side of a device's new state (RFC-007 D5):
//
//	| Action             | Wi-Fi ouvert                          | Wi-Fi fermé (or add window)          |
//	| Autoriser          | nothing on the box                    | + allow rule on each closed network  |
//	| À vérifier         | nothing on the box                    | allow rule removed (refused)         |
//	| Bloquer            | firewall rule                         | firewall rule + allow rule removed   |
//	| + déconnecter Wi-Fi| + refusal on each household network   | kept in kergui, written at the open  |
//	| Débloquer          | firewall rule and refusal removed     | firewall rule removed, flag forgotten|
//
// Authorising or checking a blocked device unblocks it. This phone can be
// neither blocked, nor « À vérifier » while the Wi-Fi is closed. The host
// records the approval itself; the allow list is built from its approvals,
// with this device's new state.
func (uc *WiFiLock) SetTrust(ctx context.Context, in WiFiTrustInput) error {
	mac := in.MAC
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in.MACFilterInput))
	if err != nil {
		return err
	}
	routerID := RouterID(in.BaseURL)
	s := &lockSession{gw: gw, routerID: routerID, baseURL: in.BaseURL}
	mode := WiFiStateOpen
	if al, ok := gw.(port.WiFiAllowList); ok {
		s.al = al
		if s.box, err = al.WiFiLockBox(ctx); err != nil {
			return err
		}
		if s.intent, err = uc.store.WiFiLockIntent(ctx, routerID); err != nil {
			return err
		}
		mode = uc.lockMode(s)
	}
	m, err := uc.members(ctx, s)
	if err != nil {
		return err
	}
	if mac == m.self && (in.To == domain.TrustBlocked || (in.To == domain.TrustToCheck && mode != WiFiStateOpen)) {
		return &domain.SelfLockoutError{Reason: domain.SelfLockoutDevice, Target: mac.String()}
	}
	name := mac.String()
	wasCut := m.cut.has(mac)

	switch in.To {
	case domain.TrustAuthorised, domain.TrustToCheck:
		if m.firewall.has(mac) {
			if err := gw.Unblock(ctx, mac); err != nil {
				return err
			}
			m.firewall.drop(mac)
			uc.log.Infof("Internet rendu à %s", name)
		}
		if wasCut {
			if err := uc.store.SetWiFiCut(ctx, routerID, mac, false); err != nil {
				return err
			}
			m.cut.drop(mac)
		}
		m.blocked.drop(mac)
		if in.To == domain.TrustAuthorised {
			m.approved.add(mac)
		} else {
			m.approved.drop(mac)
		}
		if mode != WiFiStateOpen {
			if err := uc.writeAllowList(ctx, s, m, mode); err != nil {
				return err
			}
		} else if wasCut || s.al != nil && slices.ContainsFunc(banRefusals(s.box, s.householdAPs()), mac.Equal) {
			if err := uc.unrefuse(ctx, s, m, mac); err != nil {
				return err
			}
		}
		if in.To == domain.TrustAuthorised {
			uc.log.Infof("%s autorisé", name)
		} else {
			uc.log.Infof("%s à vérifier", name)
		}
		return nil

	case domain.TrustBlocked:
		if !m.firewall.has(mac) {
			if err := blockFirewall(ctx, gw, in.BaseURL, mac, uc.purge, uc.log); err != nil {
				return err
			}
			m.firewall.add(mac)
		}
		m.blocked.add(mac)
		m.approved.drop(mac)
		if in.CutWiFi != wasCut {
			if err := uc.store.SetWiFiCut(ctx, routerID, mac, in.CutWiFi); err != nil {
				return err
			}
			if in.CutWiFi {
				m.cut.add(mac)
			} else {
				m.cut.drop(mac)
			}
		}
		switch {
		case mode != WiFiStateOpen:
			if err := uc.writeAllowList(ctx, s, m, mode); err != nil {
				return err
			}
		case in.CutWiFi:
			if err := uc.refuse(ctx, s, m, mac); err != nil {
				return err
			}
		case wasCut:
			if err := uc.unrefuse(ctx, s, m, mac); err != nil {
				return err
			}
		}
		switch {
		case in.CutWiFi && mode != WiFiStateOpen:
			uc.log.Infof("%s bloqué : internet coupé ; déconnecté du Wi-Fi (retenu dans Kergui, le Wi-Fi fermé le refuse déjà)", name)
		case in.CutWiFi:
			uc.log.Infof("%s bloqué : internet coupé et déconnecté du Wi-Fi", name)
		default:
			uc.log.Infof("%s bloqué : internet coupé", name)
		}
		return nil
	}
	return fmt.Errorf("%w: état %q inconnu", domain.ErrInvalidInput, in.To)
}

// refuse writes mac's Wi-Fi refusal while the Wi-Fi is open: in Ban through
// ApplyWiFiLock on a box with an allow list (#106), WiFiBlock elsewhere.
func (uc *WiFiLock) refuse(ctx context.Context, s *lockSession, m members, mac domain.MAC) error {
	if s.al != nil {
		return uc.writeRefusals(ctx, s, m, mac, domain.MAC{})
	}
	w, ok := s.gw.(port.WiFiAccessControl)
	if !ok {
		return fmt.Errorf("%w: adapter %s has no Wi-Fi access control", domain.ErrNotImplemented, s.gw.Meta().ID)
	}
	return refuseWithPurge(ctx, s.gw, s.baseURL, mac, uc.purge, uc.log, nil, func() error { return w.WiFiBlock(ctx, mac) })
}

// unrefuse removes mac's Wi-Fi refusal while the Wi-Fi is open.
func (uc *WiFiLock) unrefuse(ctx context.Context, s *lockSession, m members, mac domain.MAC) error {
	if s.al != nil {
		return uc.writeRefusals(ctx, s, m, domain.MAC{}, mac)
	}
	w, ok := s.gw.(port.WiFiAccessControl)
	if !ok {
		return nil // no Wi-Fi access control: nothing was refused there
	}
	return w.WiFiUnblock(ctx, mac)
}

// writeAllowList converges the closed networks to the household's allow list
// as m now says, in Allow, or in Disabled during an add window.
func (uc *WiFiLock) writeAllowList(ctx context.Context, s *lockSession, m members, mode string) error {
	boxMode := domain.WiFiModeAllow
	if mode == WiFiStateWindow {
		boxMode = domain.WiFiModeDisabled
	}
	target, err := domain.HouseholdAllowList(s.closedAPs(), boxMode, m.household())
	if err != nil {
		return err
	}
	if err := s.box.CheckFits(target); err != nil {
		uc.log.Warnf("La liste Wi-Fi de la box est pleine (%v)", err)
		return err
	}
	return s.al.ApplyWiFiLock(ctx, target)
}
