package mobilecore

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// The owner's phones (#87, parent #69). When the owner's phone sees itself on
// the box (Self), the app records that MAC on the hub as the household's
// owner device; every phone of the household fetches the list with the
// household data and hands it here (PUT /api/owner-devices), as it does the
// approvals. The core marks those devices "Owner" — approved, never new,
// spared by auto-reject — on the box's list and on the relay's list in remote
// mode, so the app, the Android watch service and auto-reject all see them
// alike.
//
// The list is kept per box for the local list, and per household
// ({"tenantId"}) for the relay's: in remote mode the phone is not on the
// household's box.

// ownerStore is a small JSON file next to the database: a few MACs per key.
type ownerStore struct {
	mu   sync.Mutex
	path string
	all  map[string][]string
}

func openOwners(dataDir string) *ownerStore {
	s := &ownerStore{path: filepath.Join(dataDir, "owner_devices.json"), all: map[string][]string{}}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, &s.all)
	}
	if s.all == nil {
		s.all = map[string][]string{}
	}
	return s
}

// tenantKey is the key of a household's list, apart from the boxes'.
func tenantKey(tenantID string) string { return "tenant:" + strings.ToLower(tenantID) }

// set replaces the list under key: the hub's list wins, a recovery included.
func (s *ownerStore) set(key string, macs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.all[key] = union(nil, macs)
	s.saveLocked()
}

// drop forgets the list under key.
func (s *ownerStore) drop(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.all[key]; ok {
		delete(s.all, key)
		s.saveLocked()
	}
}

func (s *ownerStore) saveLocked() {
	raw, err := json.Marshal(s.all)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

// get is the list under key as a set.
func (s *ownerStore) get(key string) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, m := range s.all[key] {
		out[m] = true
	}
	return out
}

// list is the list under key, sorted.
func (s *ownerStore) list(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.all[key]...)
}

// putOwnerDevices is PUT /api/owner-devices {"macs", "tenantId"?}: the
// owner's phones as the hub lists them, for the box (no tenantId) or for the
// household's remote list. Auto-reject accepts the box's at once.
func (c *Core) putOwnerDevices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MACs     []string `json:"macs"`
		TenantID string   `json:"tenantId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeJSONResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	macs := []string{}
	for _, raw := range req.MACs {
		if m, ok := canonicalMAC(raw); ok {
			macs = append(macs, m)
		}
	}
	if req.TenantID != "" {
		c.owners.set(tenantKey(req.TenantID), macs)
		writeJSONResponse(w, http.StatusOK, map[string][]string{"macs": c.owners.list(tenantKey(req.TenantID))})
		return
	}
	box := c.boxID()
	c.owners.set(box, macs)
	c.acceptOwners(r, macs)
	writeJSONResponse(w, http.StatusOK, map[string][]string{"macs": c.owners.list(box)})
}

// acceptOwners has auto-reject accept the owner's phones: a rule that is off
// remembers nothing.
func (c *Core) acceptOwners(r *http.Request, macs []string) {
	c.mu.RLock()
	url := c.routerURL
	c.mu.RUnlock()
	if c.svc.AutoReject == nil || url == "" {
		return
	}
	for _, raw := range macs {
		if m, err := domain.ParseMAC(raw); err == nil {
			_ = c.svc.AutoReject.Accept(r.Context(), url, m)
		}
	}
}

// markRemote marks the owner's phones on a device list a relay signed, and
// drops any mark the relay's own core put there: the relay's « Self » is
// not this phone. A list that is not JSON objects is returned as it came.
func (c *Core) markRemote(tenantID string, devices json.RawMessage) json.RawMessage {
	var list []map[string]any
	if json.Unmarshal(devices, &list) != nil {
		return devices
	}
	owners := c.owners.get(tenantKey(tenantID))
	for _, d := range list {
		delete(d, "Mark")
		raw, _ := d["MAC"].(string)
		if m, ok := canonicalMAC(raw); ok && owners[m] {
			d["Mark"], d["Approved"], d["NewlySeen"] = MarkOwner, true, false
		}
	}
	out, err := json.Marshal(list)
	if err != nil {
		return devices
	}
	return out
}
