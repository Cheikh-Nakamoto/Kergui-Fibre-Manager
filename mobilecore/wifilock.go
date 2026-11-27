package mobilecore

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// The closed Wi-Fi (RFC-007, #107). The routes are the API's
// (httpapi/wifilock.go); the core adds who is always in the allow list
// (coreHousehold) and the box side of an approval while the Wi-Fi is closed
// (approveThroughLock).
//
// The relays' addresses. The relay reports its own MAC to the hub in its
// observations snapshots, but the hub keeps it only for that poll's alerts and
// hands it to no phone, so nothing on the phone knows it without a change of
// the relay/hub protocol (#109 is the relay's ticket). Until then the
// household's relays are the devices the app marks as such (PUT
// /api/relay-devices, kept per box like the owner's phones), and the preview
// of a close lists the relays it knows: none while the household has a relay
// is « relais inconnu », said before anything is written. A relay left out is
// refused by the closed Wi-Fi; it is never this phone's way back in, which is
// this phone itself, always in the list.

// coreHousehold is port.HouseholdDevices for the core: the relays the app
// marked and the owner's phones the hub lists (owners.go).
type coreHousehold struct{ c *Core }

// RelayMACs implements port.HouseholdDevices.
func (h coreHousehold) RelayMACs(_ context.Context, routerID string) ([]domain.MAC, error) {
	return parseMACs(h.c.relays.list(routerID)), nil
}

// OwnerMACs implements port.HouseholdDevices.
func (h coreHousehold) OwnerMACs(_ context.Context, routerID string) ([]domain.MAC, error) {
	return parseMACs(h.c.owners.list(routerID)), nil
}

func parseMACs(raw []string) []domain.MAC {
	out := make([]domain.MAC, 0, len(raw))
	for _, s := range raw {
		if m, err := domain.ParseMAC(s); err == nil {
			out = append(out, m)
		}
	}
	return out
}

// openRelays is the store of the relays' addresses, next to the database.
func openRelays(dataDir string) *ownerStore {
	return openMACStore(filepath.Join(dataDir, "relay_devices.json"))
}

// putRelayDevices is PUT /api/relay-devices {"macs"}: the household's relays
// on the box the core points at, which the closed Wi-Fi always admits.
func (c *Core) putRelayDevices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MACs []string `json:"macs"`
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
	box := c.boxID()
	c.relays.set(box, macs)
	writeJSONResponse(w, http.StatusOK, map[string][]string{"macs": c.relays.list(box)})
}

// lockInput is the box the core points at, as the use cases take it.
func (c *Core) lockInput() usecase.MACFilterInput {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return usecase.MACFilterInput{BaseURL: c.routerCfg.BaseURL, AdapterID: c.routerCfg.AdapterID, Opts: c.routerCfg.Opts}
}

// approveThroughLock writes the box side of « Autoriser » (approved) or « À
// vérifier » while the closed Wi-Fi is involved (RFC-007 D5): the Wi-Fi is
// closed, or the device is « déconnecté du Wi-Fi ». « À vérifier » on a
// closed Wi-Fi refuses the device, so it needs Basic (D8); Autoriser is free.
// It answers the refusal itself and returns false then; true means record the
// approval.
func (c *Core) approveThroughLock(w http.ResponseWriter, r *http.Request, mac string, approved bool) bool {
	if c.wifiLockUC == nil {
		return true
	}
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return true
	}
	in := c.lockInput()
	ctx := r.Context()
	if on, err := c.wifiLockUC.Tracks(ctx, in.BaseURL, m); err != nil || !on {
		return true // open Wi-Fi: an approval writes nothing on the box
	}
	to := domain.TrustAuthorised
	if !approved {
		to = domain.TrustToCheck
		if intent, err := c.wifiLock.WiFiLockIntent(ctx, usecase.RouterID(in.BaseURL)); err == nil && intent.Closed {
			if err := c.gate.Check(ctx, entitlement.FeatureCutWiFiAccess); err != nil {
				httpapi.WriteError(w, c.log, err)
				return false
			}
		}
	}
	if err := c.wifiLockUC.SetTrust(ctx, usecase.WiFiTrustInput{MACFilterInput: in, MAC: m, To: to}); err != nil {
		httpapi.WriteError(w, c.log, err)
		return false
	}
	return true
}

// spawnLocked runs f in the background once no call holds the box: the work
// « Ouvrir mon Wi-Fi » leaves for later (the refusals written back) takes
// the box's single session like any call.
func (c *Core) spawnLocked(f func()) {
	go func() {
		c.io.Lock()
		defer c.io.Unlock()
		f()
	}()
}
