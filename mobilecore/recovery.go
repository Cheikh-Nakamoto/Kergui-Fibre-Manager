package mobilecore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Recovery of a lost owner phone, the phones' side (issue #48, RFC-005
// "Lost owner phone"; the relay's side is internal/agent/recovery.go, the
// rules internal/trust/succession.go).
//
//   - The owner names co-owners in advance: a member's key signed into the
//     bundle (SignTrustBundle's "coOwner" / "removeCoOwner").
//   - Co-owner path: the co-owner's phone takes the household over
//     (TakeOverHousehold). Its member key — the one the owner signed in —
//     endorses this phone's owner key, and this phone's owner key signs the
//     next bundle, the old owner key revoked.
//   - At home: the owner's new phone proves the box password to a relay on
//     the Wi-Fi (RecoverAtHome). The relay signs the handover; this phone
//     signs the next bundle. The address must be on the home network: the
//     proof never goes towards the Internet, where whoever sees it could try
//     passwords offline.
//
// Either way, JS uploads the bundle returned with PUT trust-bundle, like any
// other; the relays and the members check the handover against the bundle
// they already hold, so the hub can carry it but never make one up.

// Error prefixes JS reads (src/core/recovery.ts).
const (
	codeNotCoOwner        = "not_co_owner"
	codeRecoveryNotLAN    = "recovery_not_lan"
	codeRecoveryProof     = "recovery_proof"
	codeRecoveryLimited   = "recovery_rate_limited"
	codeRecoveryNotReady  = "recovery_not_ready"
	codeRecoveryNoRelay   = "recovery_unreachable"
	codeRecoveryAlready   = "recovery_already_owner"
	recoveryClientTimeout = 60 * time.Second
)

// recoveryJSON is what both paths return to JS.
type recoveryJSON struct {
	TenantID string     `json:"tenantId"`
	Bundle   signedJSON `json:"bundle"`
	// Revoked is the old owner key id, now refused by every relay.
	Revoked string `json:"revoked"`
}

// succeed signs the first bundle of this phone's owner key from held and the
// handover, and checks it the way a relay holding held will. Callers hold
// c.ownerMu.
func (c *Core) succeed(held trust.Bundle, ho trust.SignedHandover) (string, error) {
	st, id, err := c.loadOwner()
	if err != nil {
		return "", err
	}
	next, err := trust.Succeed(held, ho, st.BundleVersion, c.gate.now())
	if err != nil {
		return "", err
	}
	if next.Owner.KeyID() != id.KeyID() {
		return "", fmt.Errorf("%w: the handover names another key than this phone's", trust.ErrSuccession)
	}
	signed, err := trust.Sign(id, trust.DomainBundle, next)
	if err != nil {
		return "", err
	}
	// What every relay and member will check: refuse here rather than
	// upload a bundle they would set aside.
	if _, err := trust.CheckSuccession(held, signed); err != nil {
		return "", err
	}
	nextSt := *st
	nextSt.BundleVersion = next.Version
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if err := c.saveOwner(ctx, &nextSt); err != nil {
		return "", err
	}
	*st = nextSt
	c.log.Infof("owner: household taken over (%s, bundle v%d, key %s revoked)", handoverVia(ho), next.Version, held.Owner.KeyID())
	out, err := json.Marshal(recoveryJSON{
		TenantID: next.TenantID,
		Bundle:   signedJSON{Payload: signed.Payload, Sig: signed.Sig, Version: next.Version},
		Revoked:  held.Owner.KeyID(),
	})
	return string(out), err
}

func handoverVia(ho trust.SignedHandover) string {
	h, err := ho.Unverified()
	if err != nil {
		return "?"
	}
	return h.Via
}

// TakeOverHousehold is the co-owner path: when this phone's member key is a
// co-owner of bundle (the hub's GET trust-bundle answer), it endorses this
// phone's owner key and returns {"tenantId", "bundle": {payload, sig,
// version}, "revoked"}, the bundle to PUT. A refusal's message starts with
// "not_co_owner" when the bundle names no such co-owner (or another owner
// than the one this phone pinned).
func (c *Core) TakeOverHousehold(tenantID, bundle string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", errors.New("mobilecore: a takeover needs a household")
	}
	if strings.TrimSpace(bundle) == "" {
		return "", trust.ErrNoBundle
	}
	var cur signedJSON
	if err := json.Unmarshal([]byte(bundle), &cur); err != nil {
		return "", fmt.Errorf("%w: trust bundle: %v", trust.ErrMalformed, err)
	}
	signed := trust.Signed[trust.Bundle]{Payload: cur.Payload, Sig: cur.Sig}
	claimed, err := signed.Unverified()
	if err != nil {
		return "", err
	}

	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	mst, member, err := c.loadMember()
	if err != nil {
		return "", err
	}
	// The owner key this phone pinned, if it did; otherwise the bundle's
	// own. Either way the relays decide on the bundle they hold.
	key := claimed.Owner.SigningKey()
	if pinned := mst.Owners[tenantID]; pinned != nil && !key.Equal(ed25519.PublicKey(pinned)) {
		return "", fmt.Errorf("%s: %w: the bundle is signed by another owner than the one this phone knows", codeNotCoOwner, trust.ErrUnknownKey)
	}
	held, err := signed.Open(trust.DomainBundle, key)
	if err != nil {
		return "", err
	}
	if held.TenantID != tenantID {
		return "", fmt.Errorf("%w: the bundle is for another household", trust.ErrTenant)
	}
	if _, ok := held.CoOwner(member.KeyID()); !ok {
		return "", fmt.Errorf("%s: %w: this phone is not a co-owner of the household", codeNotCoOwner, trust.ErrUnknownKey)
	}
	_, id, err := c.loadOwner()
	if err != nil {
		return "", err
	}
	ho, err := trust.NewHandover(member, held, id.Public(), trust.ViaCoOwner, nil, c.gate.now())
	if err != nil {
		return "", err
	}
	return c.succeed(held, ho)
}

// RecoverAtHome is the path without a co-owner: at home, on the Wi-Fi, this
// phone asks the relay at relayAddress (an address on the home network, with
// or without a port, RecoveryPort by default) for a challenge, proves it
// knows boxPassword, and signs the next bundle with the handover the relay
// returns. Same answer as TakeOverHousehold. A refusal's message starts with
// "recovery_not_lan" (the address is not on a home network), "recovery_proof"
// (not the box's password), "recovery_rate_limited", "recovery_not_ready" (a
// relay not paired, or holding no password), "recovery_unreachable" or
// "recovery_already_owner".
func (c *Core) RecoverAtHome(relayAddress, boxPassword string) (string, error) {
	base, err := recoveryURL(relayAddress)
	if err != nil {
		return "", err
	}
	if boxPassword == "" {
		return "", fmt.Errorf("%s: %w", codeRecoveryProof, errors.New("mobilecore: the box password is empty"))
	}
	id, err := c.ownerIdentity()
	if err != nil {
		return "", err
	}
	client := c.recoveryClient
	if client == nil {
		client = &http.Client{Timeout: recoveryClientTimeout}
	}

	var ch agent.RecoveryChallenge
	if err := recoveryPost(client, base+"/recovery/v1/challenge", nil, &ch); err != nil {
		return "", err
	}
	held, err := selfSigned(ch.Bundle.Payload, ch.Bundle.Sig)
	if err != nil {
		return "", err
	}
	if held.TenantID != ch.TenantID {
		return "", fmt.Errorf("%w: the relay's bundle is for another household", trust.ErrTenant)
	}
	if held.Owner.KeyID() == id.KeyID() {
		return "", fmt.Errorf("%s: this phone already owns the household", codeRecoveryAlready)
	}
	pub := id.Public()
	// argon2id, ~64 MiB: the cost a password guesser would pay per guess.
	proof, err := trust.RecoveryProof(ch.TenantID, boxPassword, ch.Nonce, pub.SigningKey())
	if err != nil {
		return "", err
	}
	var grant agent.RecoveryGrant
	claim := agent.RecoveryClaim{Nonce: ch.Nonce, Ed25519: pub.Ed25519, X25519: pub.X25519, Proof: proof}
	if err := recoveryPost(client, base+"/recovery/v1/claim", claim, &grant); err != nil {
		return "", err
	}
	// The bundle in the grant is the one the relay holds now: the handover
	// is checked against it (by succeed, as the relays will).
	held, err = selfSigned(grant.Bundle.Payload, grant.Bundle.Sig)
	if err != nil {
		return "", err
	}
	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	return c.succeed(held, trust.SignedHandover{Payload: grant.Handover.Payload, Sig: grant.Handover.Sig})
}

// selfSigned opens a bundle with the owner key it names: what a new phone,
// which pinned nothing, can check. The relays check the rest.
func selfSigned(payload, sig []byte) (trust.Bundle, error) {
	s := trust.Signed[trust.Bundle]{Payload: payload, Sig: sig}
	claimed, err := s.Unverified()
	if err != nil {
		return trust.Bundle{}, err
	}
	return s.Open(trust.DomainBundle, claimed.Owner.SigningKey())
}

// recoveryURL turns what the user typed ("192.168.1.20", "192.168.1.20:8791",
// "http://…") into the relay's recovery base URL, refusing any address that
// is not on a home network: a private, link-local or loopback IP.
func recoveryURL(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	addr = strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	addr = strings.TrimSuffix(addr, "/")
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = strings.Trim(addr, "[]"), strconv.Itoa(agent.RecoveryPort)
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return "", fmt.Errorf("%s: %q is not an address on the home network", codeRecoveryNotLAN, host)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("%s: port %q", codeRecoveryNotLAN, port)
	}
	return "http://" + net.JoinHostPort(ip.String(), port), nil
}

// recoveryPost sends one step of the exchange and maps the relay's refusal
// to the prefix JS reads.
func recoveryPost(client *http.Client, url string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if body == nil {
		raw = []byte("{}")
	}
	res, err := client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%s: %w", codeRecoveryNoRelay, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("%s: %w", codeRecoveryNoRelay, err)
	}
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%w: the relay's answer: %v", trust.ErrMalformed, err)
		}
		return nil
	}
	var refusal struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(data, &refusal)
	code := codeRecoveryNoRelay
	switch refusal.Code {
	case "proof":
		code = codeRecoveryProof
	case "rate_limited":
		code = codeRecoveryLimited
	case "not_ready", "no_password":
		code = codeRecoveryNotReady
	case "not_lan":
		code = codeRecoveryNotLAN
	}
	return fmt.Errorf("%s: the relay answered %d: %s", code, res.StatusCode, refusal.Error)
}
