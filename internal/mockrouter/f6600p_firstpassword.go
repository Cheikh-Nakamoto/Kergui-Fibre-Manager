package mockrouter

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// The forced password change of a new F6600P (#92), as captured 2026-11-25.
// The wire format is restated here, not imported from the adapter (see the
// package comment of f6600p.go).

var pemBlockRe = regexp.MustCompile(`-----BEGIN PUBLIC KEY-----[^"]*-----END PUBLIC KEY-----`)

// forcedPage is the captured page with the mock's own key in place of the
// box's, and its token in place of the sanitised placeholders: hex-escaped in
// the scripts, as the box writes them, plain in the hidden input.
var forcedPage = sync.OnceValue(func() string {
	der, err := x509.MarshalPKIXPublicKey(&f6600pKey().PublicKey)
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	pemStr := strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
	page := pemBlockRe.ReplaceAllLiteralString(fixtures.ZTEF6600PForcedChangePwd, strings.ReplaceAll(pemStr, "\n", `\n`))
	page = strings.ReplaceAll(page, hexEscape("REDACTED"), hexEscape(PageToken))
	page = strings.ReplaceAll(page, `_sessionTmpToken = "REDACTED"`, `_sessionTmpToken = "`+hexEscape(PageToken)+`"`)
	return strings.ReplaceAll(page, `value="REDACTED"`, `value="`+PageToken+`"`)
})

// changePwd judges a login_changepwd form as the box would.
func (s *F6600P) changePwd(w http.ResponseWriter, r *http.Request) {
	refuse := func(msg string) {
		_, _ = w.Write([]byte(`{"login_need_refresh":false,"loginErrMsg":"` + msg + `"}`))
	}
	if !s.authed(r) || !s.PasswordChangeRequired() {
		refuse("session")
		return
	}
	_ = r.ParseForm()
	f := r.PostForm
	if f.Get("action") == "changepwd_cancel" {
		s.mu.Lock()
		s.pwdCancels++
		s.mu.Unlock()
		refuse("cancel is not supported by the mock")
		return
	}
	if f.Get("action") != "changepwd" || f.Get("Username") != s.user || f.Get("_sessionTOKEN") != PageToken {
		refuse("invalid form")
		return
	}
	key, iv, ok := decodeFormKey(f.Get("encode"))
	if !ok {
		refuse("invalid key")
		return
	}
	pw, ok1 := decryptFormValue(f.Get("Password"), key, iv)
	confirm, ok2 := decryptFormValue(f.Get("NewPassword"), key, iv)
	switch {
	case !ok1 || !ok2:
		refuse("invalid password encoding")
		return
	case pw != confirm:
		refuse("passwords differ")
		return
	case !passStrong(pw):
		refuse("weak password")
		return
	}
	s.mu.Lock()
	s.pass = pw
	s.forcePwd = false
	s.session = "" // the page reloads; the client signs in again
	s.views = map[string]bool{}
	s.lastView = ""
	s.mu.Unlock()
	_, _ = w.Write([]byte(`{"login_need_refresh":true}`))
}

// passStrong is the page's checkPassStrong: 8+ characters with a lower-case
// letter, an upper-case letter and a digit.
func passStrong(p string) bool {
	return len(p) >= 8 && strings.ContainsAny(p, "abcdefghijklmnopqrstuvwxyz") &&
		strings.ContainsAny(p, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(p, "0123456789")
}

// ChangePwdForm builds a login_changepwd form the mock accepts (for token =
// PageToken and a strong password), so a test can forge variants of it.
func ChangePwdForm(username, password, token string) url.Values {
	key, iv := "1234567890123456", "6543210987654321"
	enc, err := rsa.EncryptPKCS1v15(rand.Reader, &f6600pKey().PublicKey, []byte(key+"+"+iv))
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	secret := encryptWith(password, key, iv)
	return url.Values{
		"action": {"changepwd"}, "Password": {secret}, "Username": {username},
		"NewPassword": {secret}, "_sessionTOKEN": {token},
		"encode": {base64.StdEncoding.EncodeToString(enc)},
	}
}
