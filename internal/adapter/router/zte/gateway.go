package zte

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
}

// compile-time proof that Gateway satisfies the application's RouterPort.
var _ port.RouterPort = (*Gateway)(nil)

// New builds a Gateway for the given profile bound to baseURL.
func New(profile Profile, baseURL string, opts port.RouterOptions) (*Gateway, error) {
	s, err := httpkit.NewSession(baseURL, opts)
	if err != nil {
		return nil, err
	}
	return &Gateway{profile: profile, sess: s}, nil
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

// Block is documented but intentionally not implemented in Milestone 1 (read-only).
func (g *Gateway) Block(context.Context, domain.MAC) error { return domain.ErrNotImplemented }

// Unblock is documented but intentionally not implemented in Milestone 1.
func (g *Gateway) Unblock(context.Context, domain.MAC) error { return domain.ErrNotImplemented }

func (g *Gateway) notReady() error {
	return fmt.Errorf("%w: adapter %q has a documented profile but its parsers await a real capture (see docs/reverse-engineering)", domain.ErrNotImplemented, g.profile.ID)
}
