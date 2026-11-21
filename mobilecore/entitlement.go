package mobilecore

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// The core refuses the paid writes itself (RFC-006), so the gate does not
// rest on the screens alone: the app hands it the hub's public keys and the
// current entitlement token, and every block, Wi-Fi cut or SSID change goes
// through coreGate.Check on its way to the box. A refusal is a 403 with
// {"code": "not_entitled"}, which the app shows as a lock and one neutral
// sentence. Unblocking, restoring Wi-Fi access and turning a network back on
// are free in the tier table, so they pass with no token at all.

// SetPublicKeys sets the hub public keys the core trusts to sign
// entitlement tokens: raw 32-byte Ed25519 keys in standard base64 (as
// KERGUI_LICENSING_PUBLIC_KEY and GET /licensing/v1/public-key give them),
// separated by commas or spaces. Each is indexed by its key id, which a token
// names. "" trusts no key: paid actions are refused. An invalid list is
// refused and leaves no key trusted.
func (c *Core) SetPublicKeys(keys string) error {
	ks, err := entitlement.ParseKeySet(keys)
	c.gate.mu.Lock()
	c.gate.keys = ks // the zero set on error: fail closed
	c.gate.mu.Unlock()
	if err != nil {
		c.log.Warnf("core: licensing keys refused: %v", err)
		return err
	}
	c.log.Infof("core: licensing keys set (%d: %v)", ks.Len(), ks.IDs())
	return nil
}

// SetEntitlement hands the core the entitlement token the hub signed for
// this box ("" for none: Free). It is kept as given and checked at every paid
// action against the keys, the box serial and the clock, so a token that
// expires while the app runs stops unlocking without another call. A token
// that does not verify now is still kept (the keys may come after it) and
// only noted in the journal.
func (c *Core) SetEntitlement(token string) {
	c.gate.mu.Lock()
	c.gate.token = token
	keys := c.gate.keys
	now := c.gate.now()
	c.gate.mu.Unlock()
	if token == "" {
		c.log.Infof("core: no entitlement token (free)")
		return
	}
	cl, inGrace, err := entitlement.Verify(keys, token, now)
	if err != nil {
		c.log.Warnf("core: entitlement token does not verify: %v", err)
		return
	}
	c.log.Infof("core: entitlement %s for box %s until %s (grace=%t, key=%s)",
		cl.Tier, cl.BoxID, cl.ExpiresAt.Format(time.RFC3339), inGrace, cl.KeyID)
}

// coreGate implements httpapi.Gate for the box the core is pointed at.
type coreGate struct {
	mu    sync.Mutex
	keys  entitlement.KeySet
	token string
	// demo opens everything: the demo box is how people and store
	// reviewers see what each tier does (the app's canUse does the same).
	demo bool
	// target is the box Configure pointed at; serial its serial number once
	// read, "" until then.
	target usecase.InspectInput
	serial string

	inspect *usecase.InspectRouter
	now     func() time.Time
	log     port.Logger
}

func newGate(inspect *usecase.InspectRouter, log port.Logger) *coreGate {
	return &coreGate{inspect: inspect, now: time.Now, log: log}
}

// retarget forgets the serial of the previous box.
func (g *coreGate) retarget(in usecase.InspectInput, demo bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.target, g.serial, g.demo = in, "", demo
}

// Check implements httpapi.Gate.
func (g *coreGate) Check(ctx context.Context, f entitlement.Feature) error {
	if entitlement.Free(f) {
		return nil // never a router round trip to give access back
	}
	g.mu.Lock()
	demo, keys, token, serial, target, now := g.demo, g.keys, g.token, g.serial, g.target, g.now()
	g.mu.Unlock()
	if demo {
		return nil
	}
	if token != "" && serial == "" {
		var err error
		if serial, err = g.readSerial(ctx, target); err != nil {
			return err
		}
	}
	err := entitlement.Authorize(keys, token, serial, f, now)
	if errors.Is(err, entitlement.ErrNotEntitled) {
		g.log.Infof("core: %v", err)
	}
	return err
}

// readSerial asks the box for its serial number (the identity a pass binds
// to) and keeps it for this target. An empty one is a model whose serial
// cannot be read: no pass can bind to it.
func (g *coreGate) readSerial(ctx context.Context, target usecase.InspectInput) (string, error) {
	rep, err := g.inspect.Execute(ctx, target)
	if err != nil {
		return "", err
	}
	serial := rep.Info.SerialNumber
	g.mu.Lock()
	if g.target.BaseURL == target.BaseURL && serial != "" {
		g.serial = serial
	}
	g.mu.Unlock()
	return serial, nil
}
