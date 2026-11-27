package usecase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// MACFilterInput targets one router's global MAC filter switch.
type MACFilterInput struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	Creds     domain.Credentials
}

// MACFilter reads and sets the router's global MAC filter switch.
type MACFilter struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	disco   port.DiscoveryPort
	log     port.Logger
	clock   port.Clock // nil: KeepOnIfDue checks on every call

	// checked is when KeepOnIfDue last read each box's switch.
	mu      sync.Mutex
	checked map[string]time.Time
}

// NewMACFilter wires the interactor.
func NewMACFilter(f port.RouterFactory, v port.CredentialVault, d port.DiscoveryPort, l port.Logger) *MACFilter {
	return &MACFilter{factory: f, vault: v, disco: d, log: l, checked: map[string]time.Time{}}
}

// WithClock paces KeepOnIfDue to one read of the switch every
// FilterCheckEvery per box. It returns uc.
func (uc *MACFilter) WithClock(c port.Clock) *MACFilter {
	uc.clock = c
	return uc
}

func (uc *MACFilter) gateway(ctx context.Context, in MACFilterInput) (port.MACFilterSwitch, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return nil, err
	}
	sw, ok := gw.(port.MACFilterSwitch)
	if !ok {
		return nil, fmt.Errorf("%w: adapter %s has no MAC filter switch", domain.ErrNotImplemented, gw.Meta().ID)
	}
	return sw, nil
}

// Enabled reports whether the MAC filter is active.
func (uc *MACFilter) Enabled(ctx context.Context, in MACFilterInput) (bool, error) {
	sw, err := uc.gateway(ctx, in)
	if err != nil {
		return false, err
	}
	return sw.MACFilterEnabled(ctx)
}

// Set turns the MAC filter on or off.
func (uc *MACFilter) Set(ctx context.Context, in MACFilterInput, enabled bool) error {
	sw, err := uc.gateway(ctx, in)
	if err != nil {
		return err
	}
	return sw.SetMACFilterEnabled(ctx, enabled)
}

// SetupResult is what MACFilter.Setup did to the box.
type SetupResult string

const (
	// SetupEnabled: the filter was off and is now on, in blocklist mode.
	SetupEnabled SetupResult = "enabled"
	// SetupAlready: the filter was already on in blocklist mode; nothing done.
	SetupAlready SetupResult = "already"
	// SetupPermit: the filter is on in allowlist mode; left as the household set it.
	SetupPermit SetupResult = "permit"
	// SetupUnsupported: the box has no MAC filter switch; skipped.
	SetupUnsupported SetupResult = "unsupported"
)

// Setup turns the MAC filter on in blocklist mode (Discard) when the app or
// the relay is set up on a box: an empty blocklist cuts nobody, and the first
// cut then takes effect at once. It never switches to allowlist mode (Permit),
// which would cut every unlisted device, and leaves a box already in Permit
// as it is. Running it again on an enabled filter does nothing. A box without
// the switch is skipped (SetupUnsupported, no error).
func (uc *MACFilter) Setup(ctx context.Context, in MACFilterInput) (SetupResult, error) {
	sw, err := uc.gateway(ctx, in)
	if errors.Is(err, domain.ErrNotImplemented) {
		uc.log.Infof("Filtrage MAC : cette box n'a pas d'interrupteur, étape ignorée")
		return SetupUnsupported, nil
	}
	if err != nil {
		return "", err
	}
	var on bool
	var mode string
	if mr, ok := sw.(port.MACFilterModeReader); ok {
		on, mode, err = mr.MACFilterMode(ctx)
	} else {
		on, err = sw.MACFilterEnabled(ctx)
	}
	if errors.Is(err, domain.ErrNotImplemented) {
		uc.log.Infof("Filtrage MAC : cette box n'a pas d'interrupteur, étape ignorée")
		return SetupUnsupported, nil
	}
	if err != nil {
		return "", err
	}
	if on && mode == "Permit" {
		uc.log.Warnf("Filtrage MAC en liste blanche sur la box : laissé tel quel (seuls les appareils listés se connectent)")
		return SetupPermit, nil
	}
	if on {
		uc.log.Debugf("Filtrage MAC déjà actif (liste noire) : rien à faire")
		return SetupAlready, nil
	}
	if err := sw.SetMACFilterEnabled(ctx, true); err != nil {
		if errors.Is(err, domain.ErrNotImplemented) {
			uc.log.Infof("Filtrage MAC : cette box n'a pas d'interrupteur, étape ignorée")
			return SetupUnsupported, nil
		}
		return "", err
	}
	uc.log.Infof("Filtrage MAC activé (liste noire)")
	return SetupEnabled, nil
}

// FilterCheckEvery is how often KeepOnIfDue reads a box's filter switch: the
// phone and the relay list the devices every 30 s, and the box keeps a
// single admin session.
const FilterCheckEvery = 5 * time.Minute

// KeepOnIfDue runs KeepOn when this box's switch was last read
// FilterCheckEvery ago or more (always, without a clock). It reports whether
// the filter was switched back on.
func (uc *MACFilter) KeepOnIfDue(ctx context.Context, in MACFilterInput) (bool, error) {
	if uc.clock != nil {
		id, now := RouterID(in.BaseURL), uc.clock.Now()
		uc.mu.Lock()
		last, seen := uc.checked[id]
		due := !seen || now.Sub(last) >= FilterCheckEvery
		if due {
			uc.checked[id] = now
		}
		uc.mu.Unlock()
		if !due {
			return false, nil
		}
	}
	return uc.KeepOn(ctx, in)
}

// KeepOn switches the filter back on, in blocklist mode (Discard), when it is
// off while the box holds a block kergui wrote: off, those rules cut nobody.
// The app has no switch for it (#115); kergui manages it. A filter that is on
// is left as it is, whatever its mode, and so is one that is off with only
// the household's own rules on the box. A box without the switch is skipped.
// It reports whether the filter was switched back on.
func (uc *MACFilter) KeepOn(ctx context.Context, in MACFilterInput) (bool, error) {
	gw, err := loggedInGateway(ctx, uc.factory, uc.vault, uc.disco, routerTarget(in))
	if err != nil {
		return false, err
	}
	sw, ok := gw.(port.MACFilterSwitch)
	if !ok {
		return false, nil
	}
	on, err := sw.MACFilterEnabled(ctx)
	if errors.Is(err, domain.ErrNotImplemented) {
		return false, nil
	}
	if err != nil || on {
		return false, err
	}
	rules, err := gw.AccessRules(ctx)
	if err != nil {
		return false, err
	}
	blocks := map[domain.MAC]bool{}
	for _, r := range rules {
		if r.Mode == domain.AccessBlock && r.ByKergui && !r.MAC.IsZero() {
			blocks[r.MAC] = true
		}
	}
	if len(blocks) == 0 {
		return false, nil
	}
	// SetMACFilterEnabled always selects Discard, never Permit (port).
	if err := sw.SetMACFilterEnabled(ctx, true); err != nil {
		return false, err
	}
	uc.log.Infof("Kergui a rallumé les blocages de la box (%d %s)", len(blocks), plural(len(blocks), "appareil bloqué", "appareils bloqués"))
	return true, nil
}

func plural(n int, one, many string) string {
	if n < 2 {
		return one
	}
	return many
}
