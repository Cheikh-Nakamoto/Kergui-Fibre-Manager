package ztef6600p

// The first sign-in on a new box (#92, captured 2026-11-25 on a new F6600P).
// The login with the password printed on the label is accepted as usual, but
// GET / then answers a page that demands a new admin password instead of the
// interface: form #fLogin (Password, NewPassword), its doChgpwd posting to
// login_changepwd. Every other page stays closed until it is done.
//
// doChgpwd posts, form-urlencoded:
//
//	action        changepwd (the page's other button sends changepwd_cancel:
//	              never sent, the owner's decision Q13)
//	Password      encodeParaValue(new password, key, iv)
//	Username      the current username
//	NewPassword   encodeParaValue(confirmation, key, iv)
//	_sessionTOKEN doChgpwd's own `var sessionTmpToken = "\x.."`
//	encode        base64(RSA_PKCS1v15(key+"+"+iv)), with asyEncode's key
//
// key and iv are 16 random decimal digits; encodeParaValue is the one the SSID
// write uses (ssid.go). The answer is JSON: login_need_refresh true when the
// password was changed, else loginErrMsg.

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const changePwdURL = "/?_type=loginData&_tag=login_changepwd"

var (
	_ port.AdminPasswordChanger = (*Gateway)(nil)

	forcedUserRe = regexp.MustCompile(`id="Username"[^>]*\svalue="([^"]*)"`)
)

// forcedPassword is what the forced-change page gives to change the password.
type forcedPassword struct {
	token    string
	key      *rsa.PublicKey
	username string // the page's read-only field, for the log only
}

// isForcedPasswordPage reports whether the main page is the forced-change one.
func isForcedPasswordPage(page string) bool {
	return strings.Contains(page, "_tag=login_changepwd") && strings.Contains(page, `id="fLogin"`)
}

func parseForcedPage(page string) (*forcedPassword, error) {
	fp := &forcedPassword{}
	// doChgpwd's own token first: it is the one the page posts.
	for _, tok := range []string{parseJSString(page, "var sessionTmpToken"), parseJSString(page, "_sessionTmpToken"), extractSessionTokenFromHTML(page)} {
		if tok != "" {
			fp.token = tok
			break
		}
	}
	if fp.token == "" {
		return nil, fmt.Errorf("%w: no session token on the password-change page", domain.ErrUnexpectedResponse)
	}
	// The key asyEncode encrypts with; the first one on the page otherwise.
	if i := strings.Index(page, "function asyEncode"); i >= 0 {
		fp.key = extractRSAPublicKey(page[i:])
	}
	if fp.key == nil {
		fp.key = extractRSAPublicKey(page)
	}
	if fp.key == nil {
		return nil, fmt.Errorf("%w: no RSA key on the password-change page", domain.ErrUnexpectedResponse)
	}
	if m := forcedUserRe.FindStringSubmatch(page); m != nil {
		fp.username = html.UnescapeString(m[1])
	}
	return fp, nil
}

// changePwdForm builds doChgpwd's body, in its order, for action changepwd.
func changePwdForm(fp *forcedPassword, username, newPassword string) (string, error) {
	key, iv := randomDigits(16), randomDigits(16)
	enc, err := encodeParaValue(newPassword, key, iv)
	if err != nil {
		return "", err
	}
	encKey, err := rsaEncrypt(fp.key, []byte(key+"+"+iv))
	if err != nil {
		return "", fmt.Errorf("%w: RSA encrypt failed: %v", domain.ErrUnexpectedResponse, err)
	}
	var b strings.Builder
	add := func(k, v string) {
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k + "=" + url.QueryEscape(v))
	}
	add("action", "changepwd")
	add("Password", enc)
	add("Username", username)
	add("NewPassword", enc) // the confirmation: the same password
	add("_sessionTOKEN", fp.token)
	add("encode", base64.StdEncoding.EncodeToString(encKey))
	return b.String(), nil
}

// ChangeAdminPassword implements port.AdminPasswordChanger.
func (g *Gateway) ChangeAdminPassword(ctx context.Context, current domain.Credentials, newPassword string) error {
	if err := domain.ValidateBoxPassword(newPassword); err != nil {
		return err
	}
	err := g.Login(ctx, current)
	if err == nil {
		return fmt.Errorf("%w: the router does not ask for a new password; sign in normally", domain.ErrInvalidInput)
	}
	if !errors.Is(err, domain.ErrPasswordChangeRequired) || g.forced == nil {
		return err
	}
	if g.forced.username != "" && g.forced.username != current.Username {
		g.log.Warnf("f6600p: password-change page names user %q, signed in as %q", g.forced.username, current.Username)
	}
	body, err := changePwdForm(g.forced, current.Username, newPassword)
	if err != nil {
		return err
	}
	g.log.Infof("f6600p: setting the admin password the router asks for (user %s)", current.Username)
	resp, status, err := g.sess.PostRawWithHeaders(ctx, changePwdURL, body, map[string]string{"X-Requested-With": "XMLHttpRequest"})
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	if status != 200 {
		return fmt.Errorf("%w: login_changepwd returned %d", domain.ErrUnexpectedResponse, status)
	}
	var ans struct {
		NeedRefresh bool   `json:"login_need_refresh"`
		LoginErrMsg string `json:"loginErrMsg"`
	}
	if err := json.Unmarshal(resp, &ans); err != nil {
		return fmt.Errorf("%w: cannot parse the password-change answer: %s", domain.ErrUnexpectedResponse, truncate(string(resp), 200))
	}
	switch {
	case ans.NeedRefresh:
	case ans.LoginErrMsg != "":
		g.log.Warnf("f6600p: new admin password refused: %s", ans.LoginErrMsg)
		return fmt.Errorf("%w: %s", domain.ErrPasswordRejected, ans.LoginErrMsg)
	default:
		return fmt.Errorf("%w: password-change answer without a verdict: %s", domain.ErrUnexpectedResponse, truncate(string(resp), 200))
	}
	g.forced = nil

	// The box reloads its page here; sign in again with the new password and
	// make sure the normal interface opens.
	next := domain.Credentials{Username: current.Username, Password: newPassword}
	if err := g.Login(ctx, next); err != nil {
		if errors.Is(err, domain.ErrPasswordChangeRequired) {
			return fmt.Errorf("%w: the router accepted the new password but still asks for one", domain.ErrUnexpectedResponse)
		}
		return fmt.Errorf("the router accepted the new password, but signing in with it failed: %w", err)
	}
	g.log.Infof("f6600p: admin password changed, signed in with the new one")
	return nil
}
