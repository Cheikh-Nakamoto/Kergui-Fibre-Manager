package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/snapshot"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// snapshotJob is one kind of state the relay pushes on its own, with the
// cadence RFC-005 gives it. Each kind is independent: a box that cannot serve
// one page must not keep the others from being reported.
type snapshotJob struct {
	kind  string
	every time.Duration
	// onWrite marks the kinds worth refreshing immediately after a write.
	// Blocking a device changes the device list, not the household's SSIDs.
	onWrite bool
	build   func(*Agent, context.Context) ([]byte, error)
	last    time.Time
}

// newSnapshotJobs lists what the relay reports and how often.
func newSnapshotJobs(t Timings) []*snapshotJob {
	return []*snapshotJob{
		{kind: snapshot.KindDevices, every: t.SnapshotEvery, onWrite: true, build: buildDevices},
		{kind: snapshot.KindObservations, every: t.SnapshotEvery, onWrite: true, build: buildObservations},
		{kind: snapshot.KindSSIDs, every: t.SSIDEvery, build: buildSSIDs},
	}
}

// maybeSnapshot pushes every kind that is due, or, right after a write, the
// kinds that write changed. The hub stores them signed, so the app can open on
// the household's state without waking the box.
func (a *Agent) maybeSnapshot(ctx context.Context, afterWrite bool) {
	if !a.active {
		return
	}
	for _, job := range a.snapshots {
		if afterWrite {
			if !job.onWrite {
				continue
			}
		} else if a.now().Sub(job.last) < job.every {
			continue
		}
		if err := a.pushSnapshot(ctx, job); err != nil {
			// A snapshot that cannot be read or pushed is logged and retried
			// on the next round; it never stops the relay from serving
			// commands, which is what the household notices.
			a.log.Warnf("agent: %s snapshot: %v", job.kind, err)
			continue
		}
		job.last = a.now()
	}
}

// pushSnapshot reads one kind of state, signs it and hands it to the hub.
func (a *Agent) pushSnapshot(ctx context.Context, job *snapshotJob) error {
	body, err := job.build(a, ctx)
	if err != nil {
		return err
	}
	signed, err := trust.NewSnapshot(a.state.identity, a.state.TenantID, job.kind, a.state.AgentID, a.epoch, body, a.now())
	if err != nil {
		return fmt.Errorf("signing: %w", err)
	}
	return a.hub.postSnapshot(ctx, signed)
}

// buildDevices keeps H1's body: the /api/devices JSON exactly as the app
// already receives it.
func buildDevices(a *Agent, ctx context.Context) ([]byte, error) {
	return a.read(ctx, "/api/devices")
}

// buildObservations reports everything one poll says about each station, with
// the traffic counters merged in. This is what the hub diffs and what
// domain.Recognise reasons about.
//
// An adapter with no per-station view answers 501. The relay then derives what
// it can from the device list and says so, rather than reporting nothing: a
// household on such a box still deserves its "new device" alert, even without
// leases or radio capabilities.
func buildObservations(a *Agent, ctx context.Context) ([]byte, error) {
	out := snapshot.Observations{RelayMAC: a.relayMAC(ctx)}
	body, err := a.read(ctx, "/api/observations")
	switch {
	case err == nil:
		if err := json.Unmarshal(body, &out.Hosts); err != nil {
			return nil, fmt.Errorf("decoding observations: %w", err)
		}
	case errors.Is(err, errNotImplemented):
		hosts, derr := a.observationsFromDevices(ctx)
		if derr != nil {
			return nil, derr
		}
		out.Hosts, out.Degraded = hosts, true
	default:
		return nil, err
	}
	return json.Marshal(out)
}

// buildSSIDs reports the household's Wi-Fi networks.
func buildSSIDs(a *Agent, ctx context.Context) ([]byte, error) {
	body, err := a.read(ctx, "/api/wifi/ssids")
	if err != nil {
		return nil, err
	}
	out := snapshot.SSIDs{RelayMAC: a.relayMAC(ctx)}
	if err := json.Unmarshal(body, &out.SSIDs); err != nil {
		return nil, fmt.Errorf("decoding SSIDs: %w", err)
	}
	return json.Marshal(out)
}

// observationsFromDevices is the degraded path: one observation per device,
// with only what a device list proves.
func (a *Agent) observationsFromDevices(ctx context.Context) ([]port.ObservationView, error) {
	body, err := a.read(ctx, "/api/devices")
	if err != nil {
		return nil, err
	}
	var devices []struct {
		MAC       domain.MAC `json:"MAC"`
		IP        domain.IP  `json:"IP"`
		Hostname  string     `json:"Hostname"`
		SSID      string     `json:"SSID"`
		Connected bool       `json:"Connected"`
	}
	if err := json.Unmarshal(body, &devices); err != nil {
		return nil, fmt.Errorf("decoding devices: %w", err)
	}
	now := a.now().UTC()
	out := make([]port.ObservationView, 0, len(devices))
	for _, d := range devices {
		out = append(out, port.ObservationView{
			MAC: d.MAC.String(), At: now, IPv4: d.IP.String(),
			Hostname: d.Hostname, SSID: d.SSID, Associated: d.Connected,
		})
	}
	return out, nil
}

// relayMAC is this relay's own MAC, when it could be worked out. It is best
// effort: a relay on a network where it cannot find itself still reports.
func (a *Agent) relayMAC(ctx context.Context) string {
	mac, err := a.ownDeviceMAC(ctx)
	if err != nil {
		a.log.Warnf("agent: cannot tell which device I am: %v", err)
		return ""
	}
	return mac
}

// errNotImplemented marks a route the box's adapter cannot serve, which the
// API answers with 501.
var errNotImplemented = errors.New("agent: the router adapter does not implement this read")

// read calls one read-only route on the in-memory API and returns its body.
// It bypasses the hub's allow-list on purpose: these are the relay's own
// reads, not something the hub may ask for.
func (a *Agent) read(ctx context.Context, path string) ([]byte, error) {
	status, body := a.call(ctx, http.MethodGet, path, nil)
	switch status {
	case http.StatusOK:
		return body, nil
	case http.StatusNotImplemented:
		return nil, fmt.Errorf("%w: %s", errNotImplemented, path)
	default:
		return nil, fmt.Errorf("agent: GET %s returned %d", path, status)
	}
}
