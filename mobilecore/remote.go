package mobilecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Remote mode (issue #46, RFC-005 design rule 1): a phone signed in to a
// household whose bundle names a relay goes through the hub, even on the
// home Wi-Fi, so it never opens a second admin session on the box. JS does
// the HTTP; everything that decides whether to believe the hub is here:
//
//   - the relays are read from the bundle the owner's key signed, never from
//     the hub's list of agents;
//   - a device list is shown only once the relay's signature over it checks;
//   - a block or an unblock is signed here with the owner's key, after the
//     paid-action gate, and its outcome is believed only once the relay's
//     signature over the result checks.
//
// The hub can delay or drop what it carries; it cannot make the app show a
// device list or a "blocked ✅" no relay of the owner's signed.

// commandTTL is how long a remote command stays usable: RFC-005's maximum,
// so a slow relay round still lands.
const commandTTL = trust.MaxCommandTTL

// remoteRelayJSON is one relay of the owner's bundle, as JS sees it.
type remoteRelayJSON struct {
	AgentID string `json:"agentId"`
	Kind    string `json:"kind"`
	Label   string `json:"label,omitempty"`
}

// ownersBundle opens the hub's GET trust-bundle answer with this phone's
// owner key, for this household. Anything else is refused: a bundle the
// owner did not sign names relays the hub chose.
func (c *Core) ownersBundle(tenantID, bundle string) (trust.Bundle, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return trust.Bundle{}, errors.New("mobilecore: remote mode needs a household")
	}
	if strings.TrimSpace(bundle) == "" {
		return trust.Bundle{}, trust.ErrNoBundle
	}
	var cur signedJSON
	if err := json.Unmarshal([]byte(bundle), &cur); err != nil {
		return trust.Bundle{}, fmt.Errorf("%w: trust bundle: %v", trust.ErrMalformed, err)
	}
	id, err := c.ownerIdentity()
	if err != nil {
		return trust.Bundle{}, err
	}
	b, err := trust.Signed[trust.Bundle]{Payload: cur.Payload, Sig: cur.Sig}.Open(trust.DomainBundle, id.Public().SigningKey())
	if err != nil {
		return trust.Bundle{}, err
	}
	if b.TenantID != tenantID {
		return trust.Bundle{}, fmt.Errorf("%w: the bundle is for another household", trust.ErrTenant)
	}
	return b, nil
}

// relayByKey finds the relay of the bundle that holds keyID, unless the
// owner revoked it.
func relayByKey(b trust.Bundle, keyID string) (trust.Relay, error) {
	for _, r := range b.Revoked {
		if r == keyID {
			return trust.Relay{}, fmt.Errorf("%w: %s", trust.ErrRevoked, keyID)
		}
	}
	for _, r := range b.Relays {
		if keyID != "" && r.Keys.KeyID() == keyID {
			return r, nil
		}
	}
	return trust.Relay{}, fmt.Errorf("%w: no relay of the owner's bundle holds %s", trust.ErrUnknownKey, keyID)
}

// RemoteRelays answers whether this phone goes through the hub for the
// household: {"owner", "relays": [{agentId, kind, label}]}. owner is false
// when this phone's key did not sign bundle (another member's phone, a
// bundle the hub made up, none at all): it then stays in local mode, and
// relays is empty. A malformed bundle is an error.
func (c *Core) RemoteRelays(tenantID, bundle string) (string, error) {
	out := struct {
		Owner  bool              `json:"owner"`
		Relays []remoteRelayJSON `json:"relays"`
	}{Relays: []remoteRelayJSON{}}
	b, err := c.ownersBundle(tenantID, bundle)
	switch {
	case err == nil:
		out.Owner = true
		for _, r := range b.Relays {
			if _, err := relayByKey(b, r.Keys.KeyID()); err != nil {
				continue // revoked
			}
			out.Relays = append(out.Relays, remoteRelayJSON{AgentID: r.AgentID, Kind: r.Kind, Label: r.Label})
		}
	case errors.Is(err, trust.ErrNoBundle), errors.Is(err, trust.ErrBadSignature),
		errors.Is(err, trust.ErrUnsigned), errors.Is(err, trust.ErrTenant):
		// Not this owner's bundle: not remote mode, and not an error.
	default:
		return "", err
	}
	raw, err := json.Marshal(out)
	return string(raw), err
}

// remoteDevicesJSON is a device list a relay of the owner signed.
type remoteDevicesJSON struct {
	AgentID string          `json:"agentId"`
	AsOf    time.Time       `json:"asOf"`
	Devices json.RawMessage `json:"devices"`
}

// OpenDeviceSnapshot verifies the device list the hub serves
// (GET router/devices: its "snapshot" field, the relay's signed snapshot)
// against the relays of the owner's bundle and returns
// {"agentId", "asOf", "devices"}, devices being the /api/devices JSON the
// relay read from the box. A list no relay of the owner signed, for another
// household or of another kind, is refused.
func (c *Core) OpenDeviceSnapshot(tenantID, bundle, snapshot string) (string, error) {
	b, err := c.ownersBundle(tenantID, bundle)
	if err != nil {
		return "", err
	}
	var signed trust.SignedSnapshot
	if err := json.Unmarshal([]byte(snapshot), &signed); err != nil {
		return "", fmt.Errorf("%w: snapshot: %v", trust.ErrMalformed, err)
	}
	claimed, err := signed.Unverified()
	if err != nil {
		return "", err
	}
	relay, err := relayByKey(b, claimed.KeyID)
	if err != nil {
		return "", err
	}
	snap, err := signed.Open(trust.DomainSnapshot, relay.Keys.SigningKey())
	if err != nil {
		return "", err
	}
	switch {
	case snap.TenantID != b.TenantID:
		return "", fmt.Errorf("%w: the device list is for another household", trust.ErrTenant)
	case snap.Kind != "devices":
		return "", fmt.Errorf("%w: a %q snapshot is not a device list", trust.ErrMalformed, snap.Kind)
	case snap.AgentID != relay.AgentID:
		return "", fmt.Errorf("%w: the device list names another relay", trust.ErrMalformed)
	}
	devices := snap.Body
	if len(devices) == 0 || string(devices) == "null" {
		devices = json.RawMessage("[]")
	}
	raw, err := json.Marshal(remoteDevicesJSON{AgentID: relay.AgentID, AsOf: snap.AsOf.UTC(), Devices: devices})
	return string(raw), err
}

// signedCommandJSON is the body of POST router/devices/{mac}/{action}, plus
// what the app keeps to follow the command.
type signedCommandJSON struct {
	Payload   []byte    `json:"payload"`
	Sig       []byte    `json:"sig"`
	CmdID     string    `json:"cmdId"`
	MAC       string    `json:"mac"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// remoteActions are the device commands the app sends through the hub, and
// the paid-action gate's feature for each (blocking is Basic, unblocking
// free forever).
var remoteActions = map[string]entitlement.Feature{
	"block":   entitlement.FeatureBlock,
	"unblock": entitlement.FeatureUnblock,
}

// SignDeviceCommand signs a block or an unblock of mac with the owner's key
// for the household and returns {"payload", "sig", "cmdId", "mac",
// "expiresAt"}: payload and sig are the body of
// POST router/devices/{mac}/{action}.
//
// The paid-action gate runs first, as it does on the local path: the relay
// runs whatever the owner signed, so what the owner's core refuses to sign
// is what keeps a lapsed pass from blocking remotely. The box serial is the
// one the core read from the box this run, or else boxID, the one the app
// kept from its last reading: a remote phone does not log in to the box to
// read it again. A refusal is an error whose message starts with
// "not_entitled".
func (c *Core) SignDeviceCommand(tenantID, mac, action, boxID string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", errors.New("mobilecore: a remote command needs a household")
	}
	feature, ok := remoteActions[action]
	if !ok {
		return "", fmt.Errorf("mobilecore: %q is not a remote device command", action)
	}
	m, err := domain.ParseMAC(mac)
	if err != nil {
		return "", fmt.Errorf("mobilecore: %w", err)
	}
	if err := c.gate.checkRemote(feature, boxID); err != nil {
		if errors.Is(err, entitlement.ErrNotEntitled) {
			return "", fmt.Errorf("%s: %w", httpapi.NotEntitledCode, err)
		}
		return "", err
	}
	id, err := c.ownerIdentity()
	if err != nil {
		return "", err
	}
	now := c.gate.now()
	signed, err := trust.NewCommand(id, tenantID, http.MethodPost, "/api/devices/"+m.String()+"/"+action, nil, now, commandTTL)
	if err != nil {
		return "", err
	}
	cmd, err := signed.Unverified()
	if err != nil {
		return "", err
	}
	c.log.Infof("remote: %s %s signed (command %s)", action, m.String(), cmd.CmdID)
	raw, err := json.Marshal(signedCommandJSON{
		Payload: signed.Payload, Sig: signed.Sig, CmdID: cmd.CmdID, MAC: m.String(), ExpiresAt: cmd.ExpiresAt,
	})
	return string(raw), err
}

// remoteResultJSON is a command outcome a relay of the owner signed.
type remoteResultJSON struct {
	AgentID    string          `json:"agentId"`
	Status     int             `json:"status"`
	Body       json.RawMessage `json:"body,omitempty"`
	ExecutedAt time.Time       `json:"executedAt"`
}

// VerifyCommandResult checks the relay's signed answer to command cmdID
// (the "result" field of the hub's answer) against the relays of the owner's
// bundle, and returns {"agentId", "status", "body", "executedAt"}: status is
// what the box answered, or the relay's own refusal (403). Only a result
// that verifies may be shown as done.
func (c *Core) VerifyCommandResult(tenantID, cmdID, bundle, result string) (string, error) {
	cmdID = strings.TrimSpace(cmdID)
	if cmdID == "" {
		return "", fmt.Errorf("%w: no command id", trust.ErrMalformed)
	}
	b, err := c.ownersBundle(tenantID, bundle)
	if err != nil {
		return "", err
	}
	var signed trust.SignedResult
	if err := json.Unmarshal([]byte(result), &signed); err != nil {
		return "", fmt.Errorf("%w: result: %v", trust.ErrMalformed, err)
	}
	claimed, err := signed.Unverified()
	if err != nil {
		return "", err
	}
	relay, err := relayByKey(b, claimed.KeyID)
	if err != nil {
		return "", err
	}
	res, err := signed.Open(trust.DomainResult, relay.Keys.SigningKey())
	if err != nil {
		return "", err
	}
	if res.TenantID != b.TenantID {
		return "", fmt.Errorf("%w: the result is for another household", trust.ErrTenant)
	}
	if res.CmdID != cmdID {
		return "", fmt.Errorf("%w: the result is for another command", trust.ErrMalformed)
	}
	c.log.Infof("remote: command %s answered %d by relay %s", cmdID, res.Status, relay.AgentID)
	raw, err := json.Marshal(remoteResultJSON{AgentID: relay.AgentID, Status: res.Status, Body: res.Body, ExecutedAt: res.ExecutedAt.UTC()})
	return string(raw), err
}

// checkRemote is the gate for a command the owner signs for a relay: the
// same tier table and token as Check, without a round trip to the box.
func (g *coreGate) checkRemote(f entitlement.Feature, boxID string) error {
	if entitlement.Free(f) {
		return nil
	}
	g.mu.Lock()
	demo, keys, token, serial, now := g.demo, g.keys, g.token, g.serial, g.now()
	g.mu.Unlock()
	if demo {
		return nil
	}
	if serial == "" {
		serial = strings.ToUpper(strings.TrimSpace(boxID))
	}
	err := entitlement.Authorize(keys, token, serial, f, now)
	if errors.Is(err, entitlement.ErrNotEntitled) {
		g.log.Infof("core: remote %v", err)
	}
	return err
}
