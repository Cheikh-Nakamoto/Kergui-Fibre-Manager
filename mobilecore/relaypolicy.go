package mobilecore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// The relay policy (RFC-006, #36). The household's relay enforces
// auto-reject 24/7 only once the owner has told it to: the rule as it stands
// on the owner's phone (on/off, since when, the devices accepted there) and
// the entitlement token that pays for it, signed with the owner's key so the
// hub that carries it can neither turn the rule on nor shorten the accepted
// list. Everything in it is read here, from the core's own database and
// gate: JS passes the hub's answers in and PUTs what comes out.

// SignRelayPolicy signs the household's next relay policy and returns it as
// {"payload", "sig", "version"}, the body of PUT …/relay-policy, or "" when
// there is nothing to send:
//   - the demo runs: its rule is not the household's;
//   - bundle (the hub's GET trust-bundle answer) is empty, is not signed by
//     this phone's owner key, or is for another household: this phone is not
//     the owner, and a relay would refuse its signature anyway;
//   - current (the hub's GET relay-policy answer, "" for none) is this
//     owner's and already says the same thing.
//
// The version is one above both the hub's and the last one this phone
// signed, and is kept in the vault before the policy is returned.
func (c *Core) SignRelayPolicy(tenantID, bundle, current string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", errors.New("mobilecore: a relay policy needs a household")
	}
	c.mu.RLock()
	demo := c.demo != nil
	c.mu.RUnlock()
	if demo || strings.TrimSpace(bundle) == "" {
		return "", nil
	}
	var b signedJSON
	if err := json.Unmarshal([]byte(bundle), &b); err != nil {
		return "", fmt.Errorf("mobilecore: trust bundle: %w", err)
	}
	var cur signedJSON
	if strings.TrimSpace(current) != "" {
		if err := json.Unmarshal([]byte(current), &cur); err != nil {
			return "", fmt.Errorf("mobilecore: current relay policy: %w", err)
		}
	}

	rule, err := c.relayRule()
	if err != nil {
		return "", err
	}
	c.gate.mu.Lock()
	token := c.gate.token
	c.gate.mu.Unlock()

	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	st, id, err := c.loadOwner()
	if err != nil {
		return "", err
	}
	owned, err := trust.Signed[trust.Bundle]{Payload: b.Payload, Sig: b.Sig}.Open(trust.DomainBundle, id.Public().SigningKey())
	if err != nil || owned.TenantID != tenantID {
		return "", nil
	}

	stored := cur.Version
	if len(cur.Payload) > 0 {
		signed := trust.Signed[trust.Policy]{Payload: cur.Payload, Sig: cur.Sig}
		if p, err := signed.Unverified(); err == nil && p.Version > stored {
			stored = p.Version
		}
		if p, err := signed.Open(trust.DomainPolicy, id.Public().SigningKey()); err == nil &&
			p.TenantID == tenantID && p.Entitlement == token && sameRule(p.AutoReject, rule) {
			return "", nil
		}
	}

	now := c.gate.now()
	p := trust.Policy{
		TenantID: tenantID, Version: max(stored, st.PolicyVersion) + 1, IssuedAt: now.UTC(),
		AutoReject: rule, Entitlement: token,
	}
	signed, err := trust.Sign(id, trust.DomainPolicy, p)
	if err != nil {
		return "", err
	}
	// Kept before it is handed out, as for the bundle: a crash after the
	// upload must not let the next policy reuse this version.
	next := *st
	next.PolicyVersion = p.Version
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := c.saveOwner(ctx, &next); err != nil {
		return "", err
	}
	*st = next
	c.log.Infof("owner: relay policy v%d signed (auto-reject %t, %d accepted, token %t)",
		p.Version, rule.Enabled, len(rule.Accepted), token != "")
	out, err := json.Marshal(signedJSON{Payload: signed.Payload, Sig: signed.Sig, Version: p.Version})
	return string(out), err
}

// relayRule is the auto-reject rule of the box the core is pointed at, as
// the relay policy carries it: the accepted MACs sorted, so the same rule
// always reads the same.
func (c *Core) relayRule() (trust.AutoRejectPolicy, error) {
	c.mu.RLock()
	base := c.routerURL
	c.mu.RUnlock()
	if base == "" {
		return trust.AutoRejectPolicy{}, errors.New("mobilecore: not configured (call Configure first)")
	}
	if c.svc.AutoReject == nil {
		return trust.AutoRejectPolicy{}, domain.ErrNotImplemented
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	st, err := c.svc.AutoReject.Status(ctx, base)
	if err != nil {
		return trust.AutoRejectPolicy{}, err
	}
	rule := trust.AutoRejectPolicy{Enabled: st.Settings.Enabled, Since: st.Settings.Since.UTC()}
	for _, d := range st.Devices {
		if d.State == domain.AutoRejectAccepted {
			rule.Accepted = append(rule.Accepted, d.MAC.String())
		}
	}
	sort.Strings(rule.Accepted)
	return rule, nil
}

func sameRule(a, b trust.AutoRejectPolicy) bool {
	x, y := slices.Clone(a.Accepted), slices.Clone(b.Accepted)
	sort.Strings(x)
	sort.Strings(y)
	return a.Enabled == b.Enabled && a.Since.Equal(b.Since) && slices.Equal(x, y)
}
