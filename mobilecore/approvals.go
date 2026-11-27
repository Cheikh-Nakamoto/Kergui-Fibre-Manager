package mobilecore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// Approvals (#58). A device the household approved — « Approuver » on its
// alert or its sheet, or a name given to it — never alerts again; any other
// alerts on each arrival. The core keeps the approved MACs per box, so the
// app and the Android watch service read the same list, and marks each
// device of /api/devices with "Approved".
//
// The household's list lives on the hub when the phone is signed in to one:
// an approval made here is "unsynced" until the app has pushed it there
// (PUT /api/approvals/{mac}), and the app then replaces this list with the
// hub's (PUT /api/approvals), which marks everything synced.
//
// Owner rule (2026-11-23): the devices already there are never « new », and
// a device that joined is approved by itself once it has been on the box for
// AutoApproveDelay without being blocked or cut off — renaming it approves
// it at once. So:
//   - the first listing of a box under this rule (a new box, or an upgrade)
//     approves every device listed that is not blocked, once (Baseline);
//   - a connected device nobody approved is timed from when it was first
//     seen (Pending); past the delay it is approved, unless it was blocked
//     meanwhile (its timer is dropped), auto-reject is on (auto-reject
//     decides: it refuses it at the same delay), or the household withdrew
//     its approval (Withdrawn: only a hand approval brings it back).
//
// RFC-007 D1 (G6) amends the rule: it runs only while the Wi-Fi is open.
// While the household's Wi-Fi is closed, or an add window runs, no device
// approves itself — neither at the first listing nor after the delay — and
// the timers are dropped, so a delay starts again once the Wi-Fi is open.
// Only a hand « Autoriser » lets a device in then.
//
// The approval is the one piece of data behind « Autorisé » (RFC-007 D1):
// auto-reject's accepted set is fed from it (coreApprovals).

// DemoVisitorMAC is the device the demo makes join and leave (POST
// /api/demo/visitor), so the approval can be shown without a real arrival.
const DemoVisitorMAC = "02:4b:47:00:00:58"

const demoVisitorName = "Tablette-Visiteur"

// DemoRefusedMAC is an address the demo box refuses on the Wi-Fi while its
// list is full (POST /api/demo/list-full) and that the app never saw: the
// purge keeps it, and the « liste pleine » screen frees it by hand (#103).
const DemoRefusedMAC = "02:4b:47:00:00:59"

type boxApprovals struct {
	MACs     []string `json:"macs"`
	Unsynced []string `json:"unsynced"`
	// Baseline: the devices present under the owner rule were approved.
	Baseline bool `json:"baseline,omitempty"`
	// Pending is when each connected device nobody approved was first seen.
	Pending map[string]time.Time `json:"pending,omitempty"`
	// Withdrawn are the devices whose approval the household withdrew: never
	// approved by themselves again.
	Withdrawn []string `json:"withdrawn,omitempty"`
}

// AutoApproveDelay is how long a device that joined has to be blocked or cut
// off before it is approved by itself: the same 15 minutes as auto-reject.
const AutoApproveDelay = domain.AutoRejectDelay

// approvalStore is a small JSON file next to the database: a few MACs per
// box, written on each change.
type approvalStore struct {
	mu   sync.Mutex
	path string
	all  map[string]*boxApprovals
}

func openApprovals(dataDir string) *approvalStore {
	s := &approvalStore{path: filepath.Join(dataDir, "approvals.json"), all: map[string]*boxApprovals{}}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, &s.all)
	}
	if s.all == nil {
		s.all = map[string]*boxApprovals{}
	}
	return s
}

func (s *approvalStore) saveLocked() {
	raw, err := json.Marshal(s.all)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func (s *approvalStore) get(box string) (boxApprovals, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.all[box]
	if !ok {
		return boxApprovals{MACs: []string{}, Unsynced: []string{}}, false
	}
	return boxApprovals{MACs: append([]string{}, b.MACs...), Unsynced: append([]string{}, b.Unsynced...)}, true
}

func (s *approvalStore) record(box string) *boxApprovals {
	b, ok := s.all[box]
	if !ok {
		b = &boxApprovals{MACs: []string{}, Unsynced: []string{}}
		s.all[box] = b
	}
	return b
}

// approve adds macs to the box's list, unsynced. An empty macs still
// records the box (the upgrade is then done).
func (s *approvalStore) approve(box string, macs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.record(box)
	b.MACs = union(b.MACs, macs)
	b.Unsynced = union(b.Unsynced, macs)
	for _, m := range macs {
		b.Withdrawn = without(b.Withdrawn, m)
		delete(b.Pending, m)
	}
	s.saveLocked()
}

func (s *approvalStore) withdraw(box, mac string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.record(box)
	b.MACs = without(b.MACs, mac)
	b.Unsynced = without(b.Unsynced, mac)
	b.Withdrawn = union(b.Withdrawn, []string{mac})
	s.saveLocked()
}

// listedDevice is what the owner rule reads of a listed device.
type listedDevice struct {
	MAC       string
	Connected bool
	Blocked   bool
}

// ownerRule is what the owner rule depends on besides the listing.
type ownerRule struct {
	// autoReject: auto-reject is on for the box, and decides at the delay.
	autoReject bool
	// wifiClosed: the household's Wi-Fi is closed or an add window runs
	// (domain.WiFiLockIntent.SelfApproval is false): nobody approves itself.
	wifiClosed bool
}

// settle applies the owner rule to one listing at now and returns the MACs
// it approved.
func (s *approvalStore) settle(box string, list []listedDevice, now time.Time, rule ownerRule) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.record(box)
	if rule.wifiClosed {
		// No self-approval while closed; a delay restarts once open.
		if len(b.Pending) > 0 {
			b.Pending = nil
			s.saveLocked()
		}
		return nil
	}
	approved := map[string]bool{}
	for _, m := range b.MACs {
		approved[m] = true
	}
	withdrawn := map[string]bool{}
	for _, m := range b.Withdrawn {
		withdrawn[m] = true
	}
	var add []string
	changed := false
	if !b.Baseline {
		for _, d := range list {
			if !d.Blocked && !approved[d.MAC] {
				add = append(add, d.MAC)
			}
		}
		b.Baseline, changed = true, true
	} else {
		if b.Pending == nil {
			b.Pending = map[string]time.Time{}
		}
		for _, d := range list {
			switch {
			case approved[d.MAC] || withdrawn[d.MAC]:
				continue
			case d.Blocked:
				// Blocked: not approved by itself; timed again once unblocked.
				if _, ok := b.Pending[d.MAC]; ok {
					delete(b.Pending, d.MAC)
					changed = true
				}
			case !d.Connected:
				// Gone (or cut off the Wi-Fi): approved only while on the box.
			default:
				since, ok := b.Pending[d.MAC]
				if !ok {
					b.Pending[d.MAC] = now
					changed = true
				} else if !rule.autoReject && !now.Before(since.Add(AutoApproveDelay)) {
					add = append(add, d.MAC)
				}
			}
		}
	}
	if len(add) > 0 {
		b.MACs = union(b.MACs, add)
		b.Unsynced = union(b.Unsynced, add)
		for _, m := range add {
			delete(b.Pending, m)
		}
		changed = true
	}
	if changed {
		s.saveLocked()
	}
	return add
}

// replace is the hub's list: everything synced.
func (s *approvalStore) replace(box string, macs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.record(box)
	b.MACs = union(nil, macs)
	b.Unsynced = []string{}
	s.saveLocked()
}

// forget drops a box's record (tests: a store from before the upgrade).
func (s *approvalStore) forget(box string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.all, box)
	s.saveLocked()
}

func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, m := range b {
		found := false
		for _, x := range out {
			if x == m {
				found = true
				break
			}
		}
		if !found {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

func without(a []string, m string) []string {
	out := []string{}
	for _, x := range a {
		if x != m {
			out = append(out, x)
		}
	}
	return out
}

func canonicalMAC(raw string) (string, bool) {
	m, err := domain.ParseMAC(raw)
	if err != nil {
		return "", false
	}
	return strings.ToLower(m.String()), true
}

// boxID is the box the core points at, as the stores key it.
func (c *Core) boxID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return usecase.RouterID(c.routerURL)
}

// withApprovals serves the approval routes and marks the device list; every
// other call goes to inner.
func (c *Core) withApprovals(inner http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", inner)
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) { c.markedDevices(w, r, inner) })
	mux.HandleFunc("GET /api/approvals", func(w http.ResponseWriter, _ *http.Request) {
		b, _ := c.approvals.get(c.boxID())
		writeJSONResponse(w, http.StatusOK, b)
	})
	mux.HandleFunc("PUT /api/approvals", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MACs []string `json:"macs"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		macs := []string{}
		for _, raw := range req.MACs {
			if m, ok := canonicalMAC(raw); ok {
				macs = append(macs, m)
			}
		}
		c.approvals.replace(c.boxID(), macs)
		b, _ := c.approvals.get(c.boxID())
		writeJSONResponse(w, http.StatusOK, b)
	})
	mux.HandleFunc("GET /api/owner-devices", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResponse(w, http.StatusOK, map[string][]string{"macs": c.owners.list(c.boxID())})
	})
	mux.HandleFunc("PUT /api/owner-devices", c.putOwnerDevices)
	mux.HandleFunc("PUT /api/approvals/{mac}", func(w http.ResponseWriter, r *http.Request) { c.setApproval(w, r, true) })
	mux.HandleFunc("DELETE /api/approvals/{mac}", func(w http.ResponseWriter, r *http.Request) { c.setApproval(w, r, false) })
	mux.HandleFunc("PATCH /api/devices/{mac}", func(w http.ResponseWriter, r *http.Request) { c.renameApproves(w, r, inner) })
	mux.HandleFunc("POST /api/demo/visitor", c.demoVisitor)
	mux.HandleFunc("POST /api/demo/list-full", c.demoListFull)
	mux.HandleFunc("POST /api/import", c.importBackup)
	mux.HandleFunc("GET /api/export", c.exportBackup)
	return mux
}

func (c *Core) setApproval(w http.ResponseWriter, r *http.Request, approved bool) {
	mac, ok := canonicalMAC(r.PathValue("mac"))
	if !ok {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "not a MAC address"})
		return
	}
	box := c.boxID()
	if approved {
		c.approvals.approve(box, mac)
	} else {
		c.approvals.withdraw(box, mac)
	}
	c.log.Infof("core: %s %s", mac, map[bool]string{true: "approuvé", false: "approbation retirée"}[approved])
	writeJSONResponse(w, http.StatusOK, map[string]any{"mac": mac, "approved": approved})
}

// markedDevices is the box's list with "Approved" on each device, and
// "Mark" on this phone (self.go) and on the owner's phones (owners.go).
func (c *Core) markedDevices(w http.ResponseWriter, r *http.Request, inner http.Handler) {
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	inner.ServeHTTP(rec, r)
	var list []map[string]any
	if rec.status != http.StatusOK || json.Unmarshal([]byte(rec.body.String()), &list) != nil {
		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.status)
		_, _ = w.Write([]byte(rec.body.String()))
		return
	}
	box := c.boxID()
	c.mu.RLock()
	routerURL := c.routerURL
	c.mu.RUnlock()
	at := map[string]domain.IP{}
	for _, d := range list {
		raw, _ := d["MAC"].(string)
		ipRaw, _ := d["IP"].(string)
		if m, ok := canonicalMAC(raw); ok {
			ip, _ := domain.ParseIP(ipRaw)
			at[m] = ip
		}
	}
	// This phone (#86): approved, never « new », and out of the owner rule.
	self := c.self.selfMAC(r.Context(), routerURL, box, at)
	// The owner's phones (#87): the same, as the hub lists them.
	owners := c.owners.get(box)
	listed := make([]listedDevice, 0, len(list))
	for _, d := range list {
		raw, _ := d["MAC"].(string)
		if m, ok := canonicalMAC(raw); ok && m != self && !owners[m] {
			connected, _ := d["Connected"].(bool)
			blocked, _ := d["Blocked"].(bool)
			listed = append(listed, listedDevice{MAC: m, Connected: connected, Blocked: blocked})
		}
	}
	rule := ownerRule{autoReject: c.autoRejectOn(r.Context()), wifiClosed: !c.selfApproval(r.Context(), box)}
	if added := c.approvals.settle(box, listed, c.clock.Now(), rule); len(added) > 0 {
		c.log.Infof("core: approuvés d'office (présents, ou là depuis %s sans être bloqués) : %s", AutoApproveDelay, strings.Join(added, ", "))
	}
	b, _ := c.approvals.get(box)
	set := map[string]bool{}
	for _, m := range b.MACs {
		set[m] = true
	}
	for _, d := range list {
		raw, _ := d["MAC"].(string)
		m, ok := canonicalMAC(raw)
		d["Approved"] = ok && set[m]
		switch {
		case ok && m == self:
			d["Mark"] = MarkSelf
			d["Approved"] = true
		case ok && owners[m]:
			d["Mark"] = MarkOwner
			d["Approved"] = true
		}
		// An approved device is never « new ».
		if d["Approved"] == true {
			d["NewlySeen"] = false
		}
	}
	writeJSONResponse(w, http.StatusOK, list)
}

// selfApproval reports whether a device may approve itself on the box now:
// only while the household's Wi-Fi is open (RFC-007 D1). An intent that
// cannot be read counts as closed: a hand « Autoriser » still works.
func (c *Core) selfApproval(ctx context.Context, box string) bool {
	if c.wifiLock == nil {
		return true
	}
	intent, err := c.wifiLock.WiFiLockIntent(ctx, box)
	if err != nil {
		c.log.Warnf("core: état du Wi-Fi fermé illisible : %v", err)
		return false
	}
	return intent.SelfApproval(c.clock.Now())
}

// coreApprovalReader is the household's « Autorisé » on each box as auto-reject
// reads it (port.ApprovalReader): the approvals and the owner's phones.
type coreApprovalReader struct{ c *Core }

func (c *Core) coreApprovals() coreApprovalReader { return coreApprovalReader{c} }

// ApprovedMACs implements port.ApprovalReader.
func (r coreApprovalReader) ApprovedMACs(_ context.Context, routerID string) ([]domain.MAC, error) {
	b, _ := r.c.approvals.get(routerID)
	macs := append([]string{}, b.MACs...)
	for m := range r.c.owners.get(routerID) {
		macs = append(macs, m)
	}
	out := make([]domain.MAC, 0, len(macs))
	for _, raw := range macs {
		if m, err := domain.ParseMAC(raw); err == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// autoRejectOn reports whether auto-reject is on for the box: it then decides
// what happens to a device at the delay, not the owner rule.
func (c *Core) autoRejectOn(ctx context.Context) bool {
	c.mu.RLock()
	url := c.routerURL
	c.mu.RUnlock()
	if c.svc.AutoReject == nil || url == "" {
		return false
	}
	st, err := c.svc.AutoReject.Status(ctx, url)
	return err == nil && st.Settings.Enabled
}

// renameApproves renames through inner and, for a name that is not empty,
// approves the device: giving it a name is approving it.
func (c *Core) renameApproves(w http.ResponseWriter, r *http.Request, inner http.Handler) {
	raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	r.Body = io.NopCloser(bytes.NewReader(raw))
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	inner.ServeHTTP(rec, r)
	var req struct {
		CustomName string `json:"custom_name"`
	}
	if rec.status == http.StatusOK && json.Unmarshal(raw, &req) == nil && strings.TrimSpace(req.CustomName) != "" {
		if m, ok := canonicalMAC(r.PathValue("mac")); ok {
			c.approvals.approve(c.boxID(), m)
		}
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write([]byte(rec.body.String()))
}

// demoVisitor makes the demo's visitor join ({"present":true}) or leave the
// box. 404 outside the demo.
func (c *Core) demoVisitor(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	box, url, routerURL := c.demoBox, c.demoURL, c.routerURL
	c.mu.RUnlock()
	if box == nil || url == "" || routerURL != url {
		writeJSONResponse(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req struct {
		Present bool `json:"present"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.Present {
		box.Join(mockrouter.Station{MAC: DemoVisitorMAC, Hostname: demoVisitorName, IP: "192.168.1.58"})
	} else {
		box.Forget(DemoVisitorMAC)
	}
	writeJSONResponse(w, http.StatusOK, map[string]bool{"present": req.Present})
}

// demoListFull fills the demo box's firewall list at the count it holds, or
// lifts the limit (RFC-007 D6): « liste pleine » can be shown without a box.
// Filling it also refuses DemoRefusedMAC on the Wi-Fi, an address the app
// never saw, to be freed by hand; lifting the limit leaves that refusal.
// The demo only: 404 otherwise.
func (c *Core) demoListFull(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	box, url, routerURL := c.demoBox, c.demoURL, c.routerURL
	c.mu.RUnlock()
	if box == nil || url == "" || routerURL != url {
		writeJSONResponse(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req struct {
		Full bool `json:"full"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	limit := 0
	if req.Full {
		limit = len(box.FirewallRules())
		if !box.WiFiBlocked(DemoRefusedMAC) {
			box.SetWiFiPolicy("DEV.WIFI.AP1", "Ban")
			box.AddWiFiRule(DemoRefusedMAC, "DEV.WIFI.AP1", DemoRefusedMAC+" AP1")
		}
	}
	box.SetRuleLimits(limit, 0)
	writeJSONResponse(w, http.StatusOK, map[string]any{"full": req.Full, "limit": limit})
}

func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
