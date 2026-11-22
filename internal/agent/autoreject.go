package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// Auto-reject on the relay (RFC-006, #36). The owner's app signs a relay
// policy — the rule as it is set on the phone, and the entitlement token
// that pays for it — and the hub carries it like the trust bundle. While the
// relay holds the lease it runs the rule itself, 24/7, and says so on each
// heartbeat; the hub then tells the phones, whose watch service stands down.
// One enforcer at a time, so two admin sessions never knock each other off
// the box.
//
// The relay checks the token on its own with internal/entitlement, against
// the licensing keys it was configured with and the serial of the box it is
// in front of. Without a valid Max token it touches nothing and does not
// claim the rule, so the phone keeps enforcing it.

// AutoRejecter is the rule the relay runs, on its own box target: the same
// usecase.AutoReject the phone runs.
type AutoRejecter interface {
	Adopt(ctx context.Context, p usecase.AutoRejectPolicy) error
	Enforce(ctx context.Context) (usecase.AutoRejectRun, error)
}

// NewAutoRejecter runs uc against the relay's box.
func NewAutoRejecter(uc *usecase.AutoReject, target usecase.MACFilterInput) AutoRejecter {
	return useCaseRejecter{uc: uc, target: target}
}

type useCaseRejecter struct {
	uc     *usecase.AutoReject
	target usecase.MACFilterInput
}

func (r useCaseRejecter) Adopt(ctx context.Context, p usecase.AutoRejectPolicy) error {
	return r.uc.Adopt(ctx, r.target.BaseURL, p)
}

func (r useCaseRejecter) Enforce(ctx context.Context) (usecase.AutoRejectRun, error) {
	return r.uc.Enforce(ctx, r.target)
}

// BoxSerial reads the serial number of the box the relay is in front of: the
// identity an entitlement token binds to.
type BoxSerial func(ctx context.Context) (string, error)

// autoRejectState is what the relay remembers between rounds.
type autoRejectState struct {
	// adopted is the policy and bundle versions last handed to the rule:
	// the accepted list depends on both.
	adoptedPolicy, adoptedBundle uint64
	// serial is the box serial, read once the relay holds the lease
	// (a standby never logs in).
	serial  string
	lastRun time.Time
	// refusal is the last reason the rule did not run, logged once.
	refusal string
}

// pullPolicy installs the owner's newest relay policy.
func (a *Agent) pullPolicy(ctx context.Context) error {
	signed, err := a.hub.policy(ctx)
	if err != nil {
		return err
	}
	p, err := a.verifier.AcceptPolicy(signed)
	if err != nil {
		return err
	}
	a.state.Policy = signed
	if err := a.cfg.State.Save(a.state); err != nil {
		a.log.Warnf("agent: could not store the relay policy: %v", err)
	}
	a.log.Infof("agent: relay policy updated to version %d (auto-reject %t)", p.Version, p.AutoReject.Enabled)
	return nil
}

func (a *Agent) policyVersion() uint64 {
	if p := a.verifier.Policy(); p != nil {
		return p.Version
	}
	return 0
}

// entitled checks the policy's token for auto-reject on this box, now.
func (a *Agent) entitled(p *trust.Policy) error {
	return entitlement.Authorize(a.cfg.Licensing, p.Entitlement, a.ar.serial, entitlement.FeatureAutoReject, a.now())
}

// claimsAutoReject is what the relay tells the hub on a heartbeat: it would
// enforce the rule if it holds the lease. The hub reports it to the phones.
func (a *Agent) claimsAutoReject() bool {
	p := a.verifier.Policy()
	if a.cfg.AutoReject == nil || p == nil || !p.AutoReject.Enabled || a.ar.serial == "" {
		return false
	}
	return a.entitled(p) == nil
}

// maybeAutoReject runs one round of the rule when one is due. It is called
// only while the relay holds the lease.
func (a *Agent) maybeAutoReject(ctx context.Context) {
	p := a.verifier.Policy()
	if !a.active || a.cfg.AutoReject == nil || p == nil {
		return
	}
	if !p.AutoReject.Enabled {
		if a.ar.adoptedPolicy != p.Version {
			if err := a.cfg.AutoReject.Adopt(ctx, usecase.AutoRejectPolicy{}); err != nil {
				a.log.Warnf("agent: turning auto-reject off: %v", err)
				return
			}
			a.ar.adoptedPolicy = p.Version
		}
		return
	}
	if a.ar.serial == "" && a.cfg.BoxSerial != nil {
		serial, err := a.cfg.BoxSerial(ctx)
		if err != nil {
			a.log.Warnf("agent: cannot read the box serial for auto-reject: %v", err)
			return
		}
		a.ar.serial = serial
	}
	if err := a.entitled(p); err != nil {
		// Refused once per reason in the journal, not every round.
		if msg := err.Error(); msg != a.ar.refusal {
			a.log.Warnf("agent: auto-reject not enforced by this relay: %v", err)
			a.ar.refusal = msg
		}
		return
	}
	a.ar.refusal = ""

	now := a.now()
	if !a.ar.lastRun.IsZero() && now.Sub(a.ar.lastRun) < a.timings.SnapshotEvery {
		return
	}
	bundle := a.bundleVersion()
	if a.ar.adoptedPolicy != p.Version || a.ar.adoptedBundle != bundle {
		accepted, err := a.neverRefused(ctx, p)
		if err != nil {
			a.log.Warnf("agent: auto-reject: %v", err)
			return
		}
		if err := a.cfg.AutoReject.Adopt(ctx, usecase.AutoRejectPolicy{
			Enabled: true, Since: p.AutoReject.Since, Accepted: accepted,
		}); err != nil {
			a.log.Warnf("agent: adopting the auto-reject rule: %v", err)
			return
		}
		a.ar.adoptedPolicy, a.ar.adoptedBundle = p.Version, bundle
	}
	a.ar.lastRun = now
	run, err := a.cfg.AutoReject.Enforce(ctx)
	if err != nil {
		a.log.Warnf("agent: auto-reject round: %v", err)
		return
	}
	for _, d := range run.Refused {
		a.log.Infof("agent: auto : non accepté, %s (%s) refusé au Wi-Fi par le relais", d.DisplayName(), d.MAC)
	}
}

// neverRefused is the accepted list the relay runs the rule with: what the
// household accepted on the phone, plus what the relay must never cut — its
// own device, the household's other relays and the devices the owner
// protected (RFC-005, design rule 5).
func (a *Agent) neverRefused(ctx context.Context, p *trust.Policy) ([]domain.MAC, error) {
	var out []domain.MAC
	add := func(s string) {
		if m, err := domain.ParseMAC(s); err == nil {
			out = append(out, m)
		}
	}
	for _, m := range p.AutoReject.Accepted {
		add(m)
	}
	if b := a.verifier.Bundle(); b != nil {
		for _, m := range b.ProtectedMACs {
			add(m)
		}
		for _, r := range b.Relays {
			for _, m := range r.MACs {
				add(m)
			}
		}
	}
	own, err := a.ownDeviceMAC(ctx)
	if err != nil {
		// Without knowing which device carries the relay, the rule could
		// cut it: better not to run it.
		return nil, fmt.Errorf("cannot tell which device I am: %w", err)
	}
	if own != "" {
		add(own)
	}
	return out, nil
}

// InspectSerial reads the box serial through the inspect use case, with the
// credentials the relay holds in its vault.
func InspectSerial(uc *usecase.InspectRouter, target usecase.InspectInput) BoxSerial {
	return func(ctx context.Context) (string, error) {
		rep, err := uc.Execute(ctx, target)
		if err != nil {
			return "", err
		}
		return rep.Info.SerialNumber, nil
	}
}
