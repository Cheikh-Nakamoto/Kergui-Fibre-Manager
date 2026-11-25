package mobilecore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// The box password, sealed for the relays (issue #44, RFC-005 Trust model).
// The owner typed it once, at login; the core keeps it in its vault. Here it
// is sealed with nacl/box, owner X25519 -> relay X25519, for every relay the
// owner signed into the trust bundle, and what goes back to JS is only that
// ciphertext: the hub stores it and cannot open it, a relay opens its own
// and checks it came from the owner.
//
// The version is the owner's: it moves when the password does (the vault
// keeps a digest of the last one sealed, encrypted like the rest), so a
// relay picks a new password up from credential_version in its heartbeat,
// and sealing the same one again — at every login, for a new relay — does
// not make every relay fetch it again.

// sealedJSON is one relay's blob, the body of PUT agents/{id}/credentials.
type sealedJSON struct {
	AgentID string `json:"agentId"`
	Blob    []byte `json:"blob"`
	Version uint64 `json:"version"`
}

// SealCredentials seals the box password this core logged in with for the
// relays of bundle (the hub's GET trust-bundle answer), or only for agentID
// when it is not empty. It returns
// {"version", "boxPassword", "relays": [{"agentId", "blob", "version"}]}.
//
// The relays and their keys are read from the bundle, and only when this
// owner key signed it for this household: the hub's list of agents is not
// trusted with who gets the password. boxPassword is false, and relays
// empty, when the core holds no password for the box it is pointed at.
func (c *Core) SealCredentials(tenantID, bundle, agentID string) (string, error) {
	tenantID, agentID = strings.TrimSpace(tenantID), strings.TrimSpace(agentID)
	if tenantID == "" {
		return "", errors.New("mobilecore: sealing needs a household")
	}
	if strings.TrimSpace(bundle) == "" {
		return "", fmt.Errorf("%w: sealing needs the household's trust bundle", trust.ErrNoBundle)
	}
	var cur signedJSON
	if err := json.Unmarshal([]byte(bundle), &cur); err != nil {
		return "", fmt.Errorf("mobilecore: trust bundle: %w", err)
	}

	c.ownerMu.Lock()
	defer c.ownerMu.Unlock()
	st, id, err := c.loadOwner()
	if err != nil {
		return "", err
	}
	b, err := trust.Signed[trust.Bundle]{Payload: cur.Payload, Sig: cur.Sig}.Open(trust.DomainBundle, id.Public().SigningKey())
	if err != nil {
		return "", err
	}
	if b.TenantID != tenantID {
		return "", fmt.Errorf("%w: the bundle is for another household", trust.ErrTenant)
	}
	relays := b.Relays
	if agentID != "" {
		relays = nil
		for _, r := range b.Relays {
			if r.AgentID == agentID {
				relays = append(relays, r)
			}
		}
		if len(relays) == 0 {
			return "", fmt.Errorf("%w: relay %s is not in the trust bundle", trust.ErrUnknownKey, agentID)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	creds, ok, err := c.boxCredentials(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		out, err := json.Marshal(map[string]any{"version": st.CredentialVersion, "boxPassword": false, "relays": []sealedJSON{}})
		return string(out), err
	}

	digest := sha256.Sum256([]byte(creds.Username + "\x00" + creds.Password))
	if st.CredentialVersion == 0 || subtle.ConstantTimeCompare(digest[:], st.CredentialDigest) != 1 {
		// Kept before anything is sealed with it: a crash after an upload
		// must not let the next password reuse this version.
		next := *st
		next.CredentialVersion++
		next.CredentialDigest = digest[:]
		if err := c.saveOwner(ctx, &next); err != nil {
			return "", err
		}
		*st = next
		c.log.Infof("owner: box password is now credentials v%d", st.CredentialVersion)
	}

	sealed := make([]sealedJSON, 0, len(relays))
	for _, r := range relays {
		blob, err := trust.SealCredentials(rand.Reader, id, r.Keys.X25519, trust.Credentials{
			TenantID: tenantID, AgentID: r.AgentID,
			Username: creds.Username, Password: trust.Secret(creds.Password), Version: st.CredentialVersion,
		})
		if err != nil {
			return "", err
		}
		sealed = append(sealed, sealedJSON{AgentID: r.AgentID, Blob: blob, Version: st.CredentialVersion})
	}
	// How many and which version, never the password nor the username.
	c.log.Infof("owner: box password sealed for %d relay(s) (v%d)", len(sealed), st.CredentialVersion)
	out, err := json.Marshal(map[string]any{"version": st.CredentialVersion, "boxPassword": true, "relays": sealed})
	return string(out), err
}

// BoxPassword returns the box login the core keeps in its vault for the box it
// is pointed at, for « Mot de passe de la box » in Réglages (#93):
// {"stored": bool, "username": "...", "password": "..."}; stored is false
// (and both strings empty) when there is none. The app shows it only after
// the phone's own unlock. It is a function of the core, not an API route, on
// purpose: the API is also served on the LAN by `kergui serve`, and no route
// ever hands a router password out (brief §12).
func (c *Core) BoxPassword() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	creds, ok, err := c.boxCredentials(ctx)
	if err != nil {
		return "", err
	}
	// That it was read, never what.
	c.log.Infof("core: box password read by the app (stored=%t)", ok)
	out, err := json.Marshal(map[string]any{"stored": ok, "username": creds.Username, "password": creds.Password})
	return string(out), err
}

// boxCredentials is the login the core keeps for the box it is pointed at.
func (c *Core) boxCredentials(ctx context.Context) (creds struct{ Username, Password string }, ok bool, err error) {
	c.mu.RLock()
	base := c.routerURL
	c.mu.RUnlock()
	if base == "" {
		return creds, false, nil
	}
	got, ok, err := c.vault.Load(ctx, usecase.RouterID(base))
	if err != nil || !ok || got.Password == "" {
		return creds, false, err
	}
	creds.Username, creds.Password = got.Username, got.Password
	return creds, true, nil
}
