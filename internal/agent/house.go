package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// The whole-house commands and the SSID guard (issue #50, RFC-005 design
// rule 5): cutting Internet for the house never cuts this relay, another
// relay of the household or a protected device, and the Wi-Fi network that
// carries the relay (or a protected device) is never turned off or renamed
// from afar — either would end the remote access the command came through.

// requireVerified refuses a write whose endpoint the box model documents as
// not yet validated on hardware. An endpoint the adapter does not document
// is not refused here: the API answers 501 itself when it has none.
func (a *Agent) requireVerified(ctx context.Context, op string) error {
	if a.cfg.Endpoints == nil {
		return nil
	}
	if a.endpoints == nil {
		eps, err := a.cfg.Endpoints(ctx)
		if err != nil {
			// Fail closed: a write the relay cannot vouch for waits.
			return fmt.Errorf("%w: cannot read the box model's endpoints: %v", ErrUnverified, err)
		}
		if eps == nil {
			eps = []domain.EndpointDoc{}
		}
		a.endpoints = eps
	}
	for _, e := range a.endpoints {
		if e.Operation == op && !e.Verified {
			return fmt.Errorf("%w (%s)", ErrUnverified, op)
		}
	}
	return nil
}

// boxDevice is what the guards read of /api/devices.
type boxDevice struct {
	MAC     domain.MAC `json:"MAC"`
	SSID    string     `json:"SSID"`
	Blocked bool       `json:"Blocked"`
}

func (a *Agent) boxDevices(ctx context.Context) ([]boxDevice, error) {
	body, err := a.read(ctx, "/api/devices")
	if err != nil {
		return nil, err
	}
	var out []boxDevice
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("agent: decoding devices: %w", err)
	}
	return out, nil
}

// spared says whether the whole-house cut and the SSID guard leave a device
// alone: this relay, the household's relays and the protected devices.
func (a *Agent) spared(own, mac string) bool {
	if own != "" && sameMAC(own, mac) {
		return true
	}
	b := a.verifier.Bundle()
	return b != nil && b.IsProtected(mac)
}

// knowsItself is whether the relay can tell which device it is: from its
// LAN IP, or from the MACs the owner's bundle lists for it. Without either,
// a whole-house cut could cut the relay and is refused.
func (a *Agent) knowsItself(own string) bool {
	if own != "" {
		return true
	}
	b := a.verifier.Bundle()
	if b == nil {
		return false
	}
	for _, r := range b.Relays {
		if r.AgentID == a.state.AgentID && len(r.MACs) > 0 {
			return true
		}
	}
	return false
}

// houseResult is the answer to a whole-house cut or restore.
type houseResult struct {
	// Cut / Restored are the devices the command changed.
	Cut      []string `json:"cut,omitempty"`
	Restored []string `json:"restored,omitempty"`
	// Spared are the devices left on: the relays and the protected ones.
	Spared []string `json:"spared,omitempty"`
	// Failed are the devices the box refused.
	Failed []string `json:"failed,omitempty"`
}

// cutInternet blocks every device of the house that is not already blocked,
// except the relay, the household's relays and the protected devices.
func (a *Agent) cutInternet(ctx context.Context) (int, []byte) {
	own, err := a.ownDeviceMAC(ctx)
	if err != nil {
		return http.StatusForbidden, refusal(fmt.Errorf("%w: cannot tell which device I am: %v", ErrSelfHarm, err))
	}
	if !a.knowsItself(own) {
		return http.StatusForbidden, refusal(fmt.Errorf("%w: cannot tell which device I am", ErrSelfHarm))
	}
	devices, err := a.boxDevices(ctx)
	if err != nil {
		return http.StatusBadGateway, refusal(err)
	}
	out := houseResult{Cut: []string{}, Spared: []string{}}
	for _, d := range devices {
		mac := d.MAC.String()
		switch {
		case a.spared(own, mac):
			out.Spared = append(out.Spared, mac)
		case d.Blocked:
			// Already cut: the restore must not lift a block someone chose.
		default:
			if status, body := a.call(ctx, http.MethodPost, "/api/devices/"+mac+"/block", nil); status >= 300 {
				a.log.Warnf("agent: cut %s: %d %s", mac, status, body)
				out.Failed = append(out.Failed, mac)
				continue
			}
			out.Cut = append(out.Cut, mac)
		}
	}
	a.lastCut = append(a.lastCut, out.Cut...)
	a.log.Infof("agent: Internet cut for %d device(s), %d spared", len(out.Cut), len(out.Spared))
	b, _ := json.Marshal(out)
	return http.StatusOK, b
}

// restoreRequest names the devices to give Internet back to; empty means
// the ones this relay's last cut blocked.
type restoreRequest struct {
	MACs []string `json:"macs"`
}

// restoreInternet unblocks what a whole-house cut blocked.
func (a *Agent) restoreInternet(ctx context.Context, body []byte) (int, []byte) {
	var req restoreRequest
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return http.StatusBadRequest, refusal(fmt.Errorf("agent: restore body: %w", err))
		}
	}
	macs := req.MACs
	if len(macs) == 0 {
		macs = a.lastCut
	}
	out := houseResult{Restored: []string{}}
	for _, raw := range macs {
		m, err := domain.ParseMAC(raw)
		if err != nil {
			out.Failed = append(out.Failed, raw)
			continue
		}
		mac := m.String()
		if status, body := a.call(ctx, http.MethodPost, "/api/devices/"+mac+"/unblock", nil); status >= 300 {
			a.log.Warnf("agent: restore %s: %d %s", mac, status, body)
			out.Failed = append(out.Failed, mac)
			continue
		}
		out.Restored = append(out.Restored, mac)
	}
	a.lastCut = nil
	b, _ := json.Marshal(out)
	return http.StatusOK, b
}

// guardSSID lets a remote SSID change through only when it is an on / off
// or a rename, and never turns off or renames the network the relay or a
// protected device is on.
func (a *Agent) guardSSID(ctx context.Context, id string, body []byte) error {
	var change domain.WiFiSSIDChange
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&change); err != nil {
		return fmt.Errorf("%w: SSID change: %v", ErrNotAllowed, err)
	}
	if change.Password != nil || change.Hidden != nil {
		return fmt.Errorf("%w: only on / off and rename reach the Wi-Fi from afar", ErrNotAllowed)
	}
	turnsOff := change.Enabled != nil && !*change.Enabled
	if !turnsOff && change.Name == nil {
		return nil // turning a network on cuts nobody
	}
	raw, err := a.read(ctx, "/api/wifi/ssids")
	if err != nil {
		return fmt.Errorf("%w: cannot read the Wi-Fi networks: %v", ErrSelfHarm, err)
	}
	var ssids []domain.WiFiSSID
	if err := json.Unmarshal(raw, &ssids); err != nil {
		return fmt.Errorf("%w: decoding the Wi-Fi networks: %v", ErrSelfHarm, err)
	}
	var target *domain.WiFiSSID
	for i := range ssids {
		if ssids[i].ID == id {
			target = &ssids[i]
		}
	}
	if target == nil {
		return nil // the API answers that the network does not exist
	}
	names := func(s string) bool {
		s = strings.TrimSpace(s)
		return s != "" && (strings.EqualFold(s, target.Name) || strings.EqualFold(s, target.Alias) || strings.EqualFold(s, target.ID))
	}
	if a.cfg.WiFi != nil {
		if ssid, _ := a.cfg.WiFi(ctx); names(ssid) {
			return fmt.Errorf("%w: the relay is on %q", ErrSelfHarm, target.Name)
		}
	}
	own, err := a.ownDeviceMAC(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot tell which device I am: %v", ErrSelfHarm, err)
	}
	devices, err := a.boxDevices(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot read the devices: %v", ErrSelfHarm, err)
	}
	for _, d := range devices {
		if !names(d.SSID) {
			continue
		}
		mac := d.MAC.String()
		if own != "" && sameMAC(own, mac) {
			return fmt.Errorf("%w: the relay is on %q", ErrSelfHarm, target.Name)
		}
		if a.spared(own, mac) {
			return fmt.Errorf("%w: %s is on %q", ErrProtected, mac, target.Name)
		}
	}
	return nil
}
