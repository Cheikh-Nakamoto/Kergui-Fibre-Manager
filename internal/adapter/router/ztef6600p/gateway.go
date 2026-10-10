// Package ztef6600p implements the router adapter for the ZTE ZXHN F6600P
// (Orange Livebox Fibre). This model uses a completely different web interface
// from the older F660 family: HTTPS with self-signed cert, AJAX XML data via
// Lua pages, sha256(password+token) authentication, and a menuView-preload
// requirement for data pages. It implements port.RouterPort directly rather
// than using the zte.Profile/Gateway, since the protocol is too different.
//
// Protocol summary (observed 2026-10-10, firmware ZTEGF660006SN):
//   - Login: GET login_entry → sess_token; GET login_token → numeric token;
//     POST login_entry with sha256(password+token)
//   - Session: cookie SID_HTTPS_, no _sessionTOKEN needed for GET
//   - Data: GET menuView/<pageId> first (preload), then GET menuData/<area_lua.lua>
//   - Format: XML <ajax_response_xml_root> with <OBJ_*_ID>/<Instance>/<ParaName>/<ParaValue>
package ztef6600p

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const (
	ID            = "zte_f6600p"
	sessionCookie = "SID_HTTPS_"
)

type Gateway struct {
	sess  *httpkit.Session
	opts  port.RouterOptions
	creds domain.Credentials // retained for automatic session refresh
}

var _ port.RouterPort = (*Gateway)(nil)

func New(baseURL string, opts port.RouterOptions) (*Gateway, error) {
	s, err := httpkit.NewSession(baseURL, opts)
	if err != nil {
		return nil, err
	}
	return &Gateway{sess: s, opts: opts}, nil
}

func Register(f *router.Factory) {
	f.Register(Meta(), func(baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
		return New(baseURL, opts)
	})
}

func Meta() domain.AdapterMeta {
	return domain.AdapterMeta{
		ID:       ID,
		Vendor:   "ZTE",
		Models:   []string{"F6600P", "ZXHN F6600P"},
		Verified: true,
		Markers:  []string{"f6600p", "zxhn f6600p", "ZTEGF6600P", "&#70;&#54;&#54;&#48;&#48;&#80;"},
		Endpoints: []domain.EndpointDoc{
			{Operation: "login", Method: "POST", Path: "/?_type=loginData&_tag=login_entry", Verified: true, Notes: "sha256(password+token)"},
			{Operation: "router_info", Method: "GET", Path: "/?_type=menuData&_tag=devmgr_statusmgr_lua.lua", Verified: true},
			{Operation: "devices", Method: "GET", Path: "/?_type=menuData&_tag=wlan_client_stat_lua.lua", Verified: true},
			{Operation: "access_rules", Method: "GET", Path: "/?_type=menuData&_tag=firewall_macfilterv3_lua.lua", Verified: true},
			{Operation: "block", Method: "POST", Path: "/?_type=menuData&_tag=firewall_macfilterv3_lua.lua", Verified: false, Notes: "requires RSA integrity check header"},
			{Operation: "unblock", Method: "POST", Path: "/?_type=menuData&_tag=firewall_macfilterv3_lua.lua", Verified: false, Notes: "requires RSA integrity check header"},
		},
	}
}

func (g *Gateway) Meta() domain.AdapterMeta { return Meta() }

func (g *Gateway) Login(ctx context.Context, creds domain.Credentials) error {
	// Step 1: get session token
	body, status, err := g.sess.Get(ctx, "/?_type=loginData&_tag=login_entry")
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login_entry returned %d", domain.ErrUnexpectedResponse, status)
	}

	var sessResp struct {
		SessToken string `json:"sess_token"`
	}
	if err := json.Unmarshal(body, &sessResp); err != nil {
		return fmt.Errorf("%w: cannot parse session response: %v", domain.ErrUnexpectedResponse, err)
	}

	// Step 2: get numeric login token
	tokenBody, status, err := g.sess.Get(ctx, "/?_type=loginData&_tag=login_token")
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login_token returned %d", domain.ErrUnexpectedResponse, status)
	}
	token := parseLoginToken(string(tokenBody))
	if token == "" {
		return fmt.Errorf("%w: empty login token", domain.ErrUnexpectedResponse)
	}

	// Step 3: sha256(password + token) and POST login
	hash := sha256hex(creds.Password + token)
	loginForm := fmt.Sprintf("action=login&Username=%s&Password=%s&_sessionTOKEN=%s",
		creds.Username, hash, sessResp.SessToken)

	body, status, err = g.sess.PostRaw(ctx, "/?_type=loginData&_tag=login_entry", loginForm)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login POST returned %d", domain.ErrAuthFailed, status)
	}

	var loginResp struct {
		SessToken    string `json:"sess_token"`
		NeedRefresh  bool   `json:"login_need_refresh"`
		LoginErrMsg  string `json:"loginErrMsg"`
	}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return fmt.Errorf("%w: cannot parse login response", domain.ErrUnexpectedResponse)
	}
	if loginResp.LoginErrMsg != "" {
		return fmt.Errorf("%w: %s", domain.ErrAuthFailed, loginResp.LoginErrMsg)
	}

	// Step 4: load main page to establish full session
	if _, _, err := g.sess.Get(ctx, "/"); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}

	if !g.sess.HasCookie(sessionCookie) {
		return domain.ErrAuthFailed
	}
	g.creds = creds
	return nil
}

// menuData loads a data page, preloading the menuView if needed.
// If the session has expired and credentials are available, it automatically
// re-logs in and retries once.
func (g *Gateway) menuData(ctx context.Context, viewTag, dataTag string) ([]byte, error) {
	body, err := g.menuDataOnce(ctx, viewTag, dataTag)
	if err != nil && errors.Is(err, domain.ErrSessionExpired) && g.creds.Username != "" {
		if loginErr := g.Login(ctx, g.creds); loginErr != nil {
			return nil, fmt.Errorf("%w: session refresh failed: %v", domain.ErrSessionExpired, loginErr)
		}
		return g.menuDataOnce(ctx, viewTag, dataTag)
	}
	return body, err
}

func (g *Gateway) menuDataOnce(ctx context.Context, viewTag, dataTag string) ([]byte, error) {
	if _, _, err := g.sess.Get(ctx, "/?_type=menuView&_tag="+viewTag); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}

	body, status, err := g.sess.Get(ctx, "/?_type=menuData&_tag="+dataTag)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	switch {
	case status == 200:
		root, parseErr := parseAjaxXML(string(body))
		if parseErr == nil && !isSuccess(root) {
			return nil, fmt.Errorf("%w: %s", domain.ErrSessionExpired, root.ErrorStr)
		}
		return body, nil
	case status == 401 || status == 403:
		return nil, domain.ErrSessionExpired
	default:
		return nil, fmt.Errorf("%w: status %d", domain.ErrUnexpectedResponse, status)
	}
}

func (g *Gateway) RouterInfo(ctx context.Context) (*domain.RouterInfo, error) {
	body, err := g.menuData(ctx, "statusMgr", "devmgr_statusmgr_lua.lua")
	if err != nil {
		return nil, err
	}
	model, fw, hw := parseRouterInfo(string(body))
	return &domain.RouterInfo{
		BaseURL:      g.sess.BaseURL(),
		Vendor:       "ZTE",
		Model:        model,
		Firmware:     fw,
		HardwareVer:  hw,
		AdapterID:    ID,
		Reachable:    true,
		DiscoveredAt: time.Now(),
	}, nil
}

func (g *Gateway) Devices(ctx context.Context) ([]domain.Device, error) {
	body, err := g.menuData(ctx, "localNetStatus", "wlan_client_stat_lua.lua")
	if err != nil {
		return nil, err
	}
	devs := parseWLANClients(string(body))

	// Cross-reference ACL to flag blocked devices
	if rules, err := g.AccessRules(ctx); err == nil {
		blocked := make(map[string]bool, len(rules))
		for _, r := range rules {
			if r.Mode == domain.AccessBlock {
				blocked[r.MAC.String()] = true
			}
		}
		for i := range devs {
			if blocked[devs[i].MAC.String()] {
				devs[i].Blocked = true
			}
		}
	}
	return devs, nil
}

func (g *Gateway) AccessRules(ctx context.Context) ([]domain.AccessRule, error) {
	body, err := g.menuData(ctx, "filterCriteria", "firewall_macfilterv3_lua.lua")
	if err != nil {
		return nil, err
	}
	return parseMACFilter(string(body)), nil
}

func (g *Gateway) Block(ctx context.Context, mac domain.MAC) error {
	return g.changeACL(ctx, mac, true)
}

func (g *Gateway) Unblock(ctx context.Context, mac domain.MAC) error {
	return g.changeACL(ctx, mac, false)
}

func (g *Gateway) changeACL(_ context.Context, _ domain.MAC, _ bool) error {
	return fmt.Errorf("%w: adapter %q write path requires RSA integrity check (not yet implemented)", domain.ErrNotImplemented, ID)
}

// verifyACL rereads the ACL and confirms the expected state.
func (g *Gateway) verifyACL(ctx context.Context, mac domain.MAC, want bool, failErr error) error {
	rules, err := g.AccessRules(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNotImplemented) {
			return nil
		}
		return err
	}
	present := false
	for _, r := range rules {
		if r.MAC.Equal(mac) && r.Mode == domain.AccessBlock {
			present = true
			break
		}
	}
	if present != want {
		return fmt.Errorf("%w: router accepted the request but the ACL does not reflect it", failErr)
	}
	return nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
