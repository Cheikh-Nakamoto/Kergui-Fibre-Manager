package mobilecore

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

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
// Upgrade: the first listing of a box this store has no record of approves
// every device that already has a custom name, once.

// DemoVisitorMAC is the device the demo makes join and leave (POST
// /api/demo/visitor), so the approval can be shown without a real arrival.
const DemoVisitorMAC = "02:4b:47:00:00:58"

const demoVisitorName = "Tablette-Visiteur"

type boxApprovals struct {
	MACs     []string `json:"macs"`
	Unsynced []string `json:"unsynced"`
}

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
	s.saveLocked()
}

func (s *approvalStore) withdraw(box, mac string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.record(box)
	b.MACs = without(b.MACs, mac)
	b.Unsynced = without(b.Unsynced, mac)
	s.saveLocked()
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
	mux.HandleFunc("PUT /api/approvals/{mac}", func(w http.ResponseWriter, r *http.Request) { c.setApproval(w, r, true) })
	mux.HandleFunc("DELETE /api/approvals/{mac}", func(w http.ResponseWriter, r *http.Request) { c.setApproval(w, r, false) })
	mux.HandleFunc("PATCH /api/devices/{mac}", func(w http.ResponseWriter, r *http.Request) { c.renameApproves(w, r, inner) })
	mux.HandleFunc("POST /api/demo/visitor", c.demoVisitor)
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

// markedDevices is the box's list with "Approved" on each device.
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
	b, known := c.approvals.get(box)
	if !known {
		// The upgrade: devices named before approvals existed are approved.
		named := []string{}
		for _, d := range list {
			name, _ := d["CustomName"].(string)
			raw, _ := d["MAC"].(string)
			if m, ok := canonicalMAC(raw); ok && strings.TrimSpace(name) != "" {
				named = append(named, m)
			}
		}
		c.approvals.approve(box, named...)
		b, _ = c.approvals.get(box)
	}
	set := map[string]bool{}
	for _, m := range b.MACs {
		set[m] = true
	}
	for _, d := range list {
		raw, _ := d["MAC"].(string)
		m, ok := canonicalMAC(raw)
		d["Approved"] = ok && set[m]
	}
	writeJSONResponse(w, http.StatusOK, list)
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

func writeJSONResponse(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
