package zte

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Gateway is a profile-driven ZTE ZXHN router gateway. One implementation serves
// every ZTE model; the Profile supplies the differences.
type Gateway struct {
	profile Profile
	sess    *httpkit.Session
	opts    port.RouterOptions
}

// compile-time proof that Gateway satisfies the application's RouterPort.
var _ port.RouterPort = (*Gateway)(nil)

// New builds a Gateway for the given profile bound to baseURL.
func New(profile Profile, baseURL string, opts port.RouterOptions) (*Gateway, error) {
	s, err := httpkit.NewSession(baseURL, opts)
	if err != nil {
		return nil, err
	}
	return &Gateway{profile: profile, sess: s, opts: opts}, nil
}

// Meta returns the adapter metadata.
func (g *Gateway) Meta() domain.AdapterMeta { return g.profile.Meta() }

// Login fetches the login page (for its token), submits the credentials, and
// treats the presence of the session cookie as success.
func (g *Gateway) Login(ctx context.Context, creds domain.Credentials) error {
	if !g.profile.ReadReady {
		return g.notReady()
	}
	body, status, err := g.sess.Get(ctx, g.profile.LoginPage)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login page returned status %d", domain.ErrUnexpectedResponse, status)
	}

	f := g.profile.Fields
	token := extractLoginToken(string(body), f.Token)
	form := url.Values{}
	if f.Username != "" {
		form.Set(f.Username, creds.Username)
	}
	if f.Password != "" {
		form.Set(f.Password, g.password(creds.Password, token))
	}
	if f.Token != "" {
		form.Set(f.Token, token)
	}
	if f.Action != "" {
		form.Set(f.Action, f.ActionValue)
	}
	for k, v := range f.Extra {
		form.Set(k, v)
	}

	if _, _, err := g.sess.PostForm(ctx, g.profile.LoginSubmit, form); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if g.sess.HasCookie(g.profile.SessionCookie) {
		return nil
	}
	return domain.ErrAuthFailed
}

func (g *Gateway) password(pw, token string) string {
	switch g.profile.PasswordMode {
	case PasswordSHA256:
		return sha256hex(pw)
	case PasswordSHA256Token:
		return sha256hex(pw + token)
	default:
		return pw
	}
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (g *Gateway) authedGet(ctx context.Context, rel string) ([]byte, error) {
	body, status, err := g.sess.Get(ctx, rel)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	switch {
	case status == 200:
		return body, nil
	case status == 401 || status == 403:
		return nil, domain.ErrSessionExpired
	default:
		return nil, fmt.Errorf("%w: status %d", domain.ErrUnexpectedResponse, status)
	}
}

// RouterInfo reads the device-information page.
func (g *Gateway) RouterInfo(ctx context.Context) (*domain.RouterInfo, error) {
	if !g.profile.ReadReady {
		return nil, g.notReady()
	}
	body, err := g.authedGet(ctx, g.profile.StatusPage)
	if err != nil {
		return nil, err
	}
	model, fw, hw := parseRouterInfo(string(body))
	return &domain.RouterInfo{
		BaseURL:      g.sess.BaseURL(),
		Vendor:       g.profile.Vendor,
		Model:        model,
		Firmware:     fw,
		HardwareVer:  hw,
		AdapterID:    g.profile.ID,
		Reachable:    true,
		DiscoveredAt: time.Now(),
	}, nil
}

// Devices lists connected devices and marks those on the block list.
func (g *Gateway) Devices(ctx context.Context) ([]domain.Device, error) {
	if !g.profile.ReadReady {
		return nil, g.notReady()
	}
	body, err := g.authedGet(ctx, g.profile.DevicesPage)
	if err != nil {
		return nil, err
	}
	devs := parseDevices(string(body))

	// Best-effort: cross-reference the ACL to flag blocked devices. Inventory and
	// access control stay separate models (brief §18); this only sets a display
	// flag on the inventory view.
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

// AccessRules reads the access-control list.
func (g *Gateway) AccessRules(ctx context.Context) ([]domain.AccessRule, error) {
	if !g.profile.ReadReady {
		return nil, g.notReady()
	}
	body, err := g.authedGet(ctx, g.profile.ACLPage)
	if err != nil {
		return nil, err
	}
	return parseAccessRules(string(body)), nil
}

// Block adds a MAC to the router's block list.
func (g *Gateway) Block(ctx context.Context, mac domain.MAC) error {
	return g.changeACL(ctx, mac, true)
}

// Unblock removes a MAC from the router's block list.
func (g *Gateway) Unblock(ctx context.Context, mac domain.MAC) error {
	return g.changeACL(ctx, mac, false)
}

// changeACL performs an access-control write. It is gated by Profile.WriteReady:
// a model whose write path has not been wired returns ErrNotImplemented rather
// than firing an unknown request at the router. The endpoint and fields come
// from the profile and remain UNVERIFIED until confirmed on hardware; a real
// login session (cookie) is required, exactly as the web UI would do.
//
// When the profile declares a WriteTokenPage, the gateway scrapes a per-request
// CSRF token before posting — and refuses to post if the token is not found.
// After a successful POST, the gateway rereads the ACL to confirm the change
// took effect (read-after-write verification), unless the profile or the caller
// opts out.
func (g *Gateway) changeACL(ctx context.Context, mac domain.MAC, add bool) error {
	failErr := domain.ErrUnblockFailed
	if add {
		failErr = domain.ErrBlockFailed
	}
	if !g.profile.WriteReady {
		return fmt.Errorf("%w: adapter %q has no wired write path yet (see docs/reverse-engineering)", domain.ErrNotImplemented, g.profile.ID)
	}
	if mac.IsZero() {
		return domain.ErrInvalidMAC
	}

	// Optional per-request CSRF token.
	var writeToken string
	if g.profile.WriteTokenPage != "" && g.profile.WriteTokenField != "" {
		body, err := g.authedGet(ctx, g.profile.WriteTokenPage)
		if err != nil {
			return err
		}
		writeToken = extractToken(string(body), g.profile.WriteTokenField, g.profile.WriteTokenVar)
		if writeToken == "" {
			return fmt.Errorf("%w: write token %q not found on %s",
				domain.ErrUnexpectedResponse, g.profile.WriteTokenField, g.profile.WriteTokenPage)
		}
	}

	wf := g.profile.Write
	form := url.Values{}
	if wf.Action != "" {
		if add {
			form.Set(wf.Action, wf.AddValue)
		} else {
			form.Set(wf.Action, wf.DelValue)
		}
	}
	if wf.MAC != "" {
		form.Set(wf.MAC, mac.String())
	}
	if wf.Mode != "" {
		form.Set(wf.Mode, wf.ModeVal)
	}
	if writeToken != "" {
		form.Set(g.profile.WriteTokenField, writeToken)
	}

	_, status, err := g.sess.PostForm(ctx, g.profile.WritePath, form)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	switch {
	case status == 200:
		// Read-after-write: confirm the ACL actually changed.
		if g.profile.NoWriteVerify || g.opts.SkipWriteVerify {
			return nil
		}
		return g.verifyACL(ctx, mac, add, failErr)
	case status == 401 || status == 403:
		return domain.ErrSessionExpired
	default:
		return fmt.Errorf("%w: router returned status %d", failErr, status)
	}
}

// verifyACL rereads the access-control list and confirms that the MAC is present
// (want=true, after a block) or absent (want=false, after an unblock). This is
// the only proof that a write actually took effect: some ZTE firmwares respond
// 200 to a POST they silently ignore.
func (g *Gateway) verifyACL(ctx context.Context, mac domain.MAC, want bool, failErr error) error {
	rules, err := g.AccessRules(ctx)
	if err != nil {
		// If the model cannot read its own ACL (ErrNotImplemented), do not
		// turn a potentially successful write into an error.
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
		return fmt.Errorf("%w: router accepted the request (HTTP 200) but the ACL does not reflect it", failErr)
	}
	return nil
}

func (g *Gateway) notReady() error {
	return fmt.Errorf("%w: adapter %q has a documented profile but its parsers await a real capture (see docs/reverse-engineering)", domain.ErrNotImplemented, g.profile.ID)
}
