package mobilecore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/entitlement"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Remote consumption, SSIDs, Wi-Fi access and cut / restore Internet for the
// whole house (issue #50). The same rules as block / unblock (remote.go):
// the core signs, after the paid-action gate, with the owner's key — or a
// member's own, under the owner's delegation — and the app sends the result
// to POST router/commands. The relay guards what runs (internal/agent).

// signedRequestJSON is the body of POST router/commands, plus what the app
// keeps to follow the command.
type signedRequestJSON struct {
	Payload   []byte `json:"payload"`
	Sig       []byte `json:"sig"`
	Body      []byte `json:"body,omitempty"`
	CmdID     string `json:"cmdId"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	ExpiresAt string `json:"expiresAt"`
}

// remoteRequestFeatures is the gate for one remote request (RFC-006): reads
// are free (the current month only: the relay's allow-list has no period),
// cutting Wi-Fi access or Internet is Basic, restoring is free, turning a
// network off or renaming it is Max, turning one on free. Anything else is
// not a remote request.
func remoteRequestFeatures(method, path string, body []byte) ([]entitlement.Feature, error) {
	bad := fmt.Errorf("mobilecore: %s %s is not a remote request", method, path)
	if strings.ContainsAny(path, "?#") || strings.Contains(path, "..") {
		return nil, bad
	}
	switch method {
	case http.MethodGet:
		switch path {
		case "/api/devices", "/api/wifi/ssids", "/api/wifi/blocked", "/api/usage":
			return nil, nil
		}
	case http.MethodPost:
		switch path {
		case agent.RouteInternetCut:
			return []entitlement.Feature{entitlement.FeatureBlock}, nil
		case agent.RouteInternetRestore:
			return []entitlement.Feature{entitlement.FeatureUnblock}, nil
		}
		if rest, ok := strings.CutPrefix(path, "/api/devices/"); ok {
			mac, action, _ := strings.Cut(rest, "/")
			if _, err := domain.ParseMAC(mac); err != nil {
				return nil, bad
			}
			switch action {
			case "wifi-block":
				return []entitlement.Feature{entitlement.FeatureCutWiFiAccess}, nil
			case "wifi-unblock":
				return []entitlement.Feature{entitlement.FeatureRestoreWiFiAccess}, nil
			}
		}
	case http.MethodPatch:
		id, ok := strings.CutPrefix(path, "/api/wifi/ssids/")
		if !ok || id == "" || strings.Contains(id, "/") {
			return nil, bad
		}
		var c domain.WiFiSSIDChange
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("mobilecore: SSID change: %w", err)
		}
		if c.Password != nil || c.Hidden != nil {
			return nil, errors.New("mobilecore: only on / off and rename go through the relay")
		}
		var fs []entitlement.Feature
		if c.Enabled != nil && *c.Enabled {
			fs = append(fs, entitlement.FeatureNetworkOn)
		}
		if (c.Enabled != nil && !*c.Enabled) || c.Name != nil {
			fs = append(fs, entitlement.FeatureSSIDManage)
		}
		if len(fs) == 0 {
			return nil, errors.New("mobilecore: an empty SSID change")
		}
		return fs, nil
	}
	return nil, bad
}

// SignRemoteRequest signs one request for the household's relay and returns
// {"payload", "sig", "body", "cmdId", "method", "path", "expiresAt"}: the
// first three are the body of POST router/commands. method and path must be
// a remote request (remoteRequestFeatures); body is the JSON the relay
// passes on ("" for none).
//
// The owner's phone signs with the owner key; another phone with its member
// key, only under an active control delegation for a write ("owner_only",
// "not_delegated" otherwise; a view delegation may read). The paid-action
// gate runs first ("not_entitled"), on the box serial the core read or else
// boxID.
func (c *Core) SignRemoteRequest(tenantID, bundle, method, path, body, boxID string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", errors.New("mobilecore: a remote command needs a household")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	var raw []byte
	if strings.TrimSpace(body) != "" {
		raw = []byte(body)
	}
	features, err := remoteRequestFeatures(method, path, raw)
	if err != nil {
		return "", err
	}
	write := method != http.MethodGet

	var id *trust.Identity
	if _, oerr := c.ownersBundle(tenantID, bundle); oerr == nil {
		if id, err = c.ownerIdentity(); err != nil {
			return "", err
		}
	} else if !notOwners(oerr) {
		return "", oerr
	} else {
		mb, merr := c.membersBundle(tenantID, bundle)
		if merr != nil {
			return "", fmt.Errorf("%s: %w", codeNotDelegated, merr)
		}
		switch {
		case mb.view.State == memberRevoked:
			return "", fmt.Errorf("%s: %w", codeNotDelegated, trust.ErrRevoked)
		case mb.view.State == memberExpired:
			return "", fmt.Errorf("%s: %w", codeNotDelegated, trust.ErrDelegationExpired)
		case mb.view.State != memberActive:
			return "", fmt.Errorf("%s: %w", codeNotDelegated, trust.ErrUnknownKey)
		case write && mb.bundle.OwnerOnly:
			return "", fmt.Errorf("%s: %w", codeOwnerOnly, trust.ErrOwnerOnly)
		case write && mb.delegation.Scope != trust.ScopeControl:
			return "", fmt.Errorf("%s: %w: view may not write", codeNotDelegated, trust.ErrScope)
		}
		c.ownerMu.Lock()
		_, mid, lerr := c.loadMember()
		c.ownerMu.Unlock()
		if lerr != nil {
			return "", lerr
		}
		id = mid
	}
	for _, f := range features {
		if err := c.gate.checkRemote(f, boxID); err != nil {
			if errors.Is(err, entitlement.ErrNotEntitled) {
				return "", fmt.Errorf("%s: %w", httpapi.NotEntitledCode, err)
			}
			return "", err
		}
	}
	signed, err := trust.NewCommand(id, tenantID, method, path, raw, c.gate.now(), commandTTL)
	if err != nil {
		return "", err
	}
	cmd, err := signed.Unverified()
	if err != nil {
		return "", err
	}
	c.log.Infof("remote: %s %s signed (command %s)", method, path, cmd.CmdID)
	out, err := json.Marshal(signedRequestJSON{
		Payload: signed.Payload, Sig: signed.Sig, Body: raw, CmdID: cmd.CmdID,
		Method: method, Path: path, ExpiresAt: cmd.ExpiresAt.UTC().Format(timeLayout),
	})
	return string(out), err
}
