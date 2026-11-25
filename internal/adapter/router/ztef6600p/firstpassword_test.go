package ztef6600p

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// The page captured on a new F6600P right after the first sign-in (#92).
var forcedPage = fixtures.ZTEF6600PForcedChangePwd

// asyEncodePEMStart is the start of the key the captured page's asyEncode
// encrypts with, as it is written there.
const asyEncodePEMStart = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAodPTerkUVCYmv28SOfRV"

func TestIsForcedPasswordPage(t *testing.T) {
	if !isForcedPasswordPage(forcedPage) {
		t.Fatal("the captured forced-change page is not recognised")
	}
	for name, page := range map[string]string{"main page": mockMainPage, "preempt page": preemptPage} {
		if isForcedPasswordPage(page) {
			t.Errorf("%s taken for the forced-change page", name)
		}
	}
}

func TestParseForcedPage_Fixture(t *testing.T) {
	fp, err := parseForcedPage(forcedPage)
	if err != nil {
		t.Fatal(err)
	}
	// The sanitised capture hex-escapes "REDACTED" in doChgpwd's own
	// sessionTmpToken, as the box does with the real one.
	if fp.token != "REDACTED" {
		t.Errorf("token = %q, want REDACTED", fp.token)
	}
	if fp.username != "user" {
		t.Errorf("username = %q, want user (the page's read-only field)", fp.username)
	}
	if fp.key == nil || fp.key.N.BitLen() != 2048 {
		t.Fatalf("asyEncode key not read: %v", fp.key)
	}
	want := extractRSAPublicKey(forcedPage[strings.Index(forcedPage, asyEncodePEMStart)-len("-----BEGIN PUBLIC KEY-----\\n"):])
	if want == nil || want.N.Cmp(fp.key.N) != 0 {
		t.Error("the key read is not the one asyEncode uses")
	}
}

// doChgpwd posts its local sessionTmpToken, not the page-wide
// _sessionTmpToken: when the two differ, the local one wins.
func TestParseForcedPage_PrefersDoChgpwdToken(t *testing.T) {
	page := strings.Replace(forcedPage, `var sessionTmpToken = "\x52\x45\x44\x41\x43\x54\x45\x44";`, `var sessionTmpToken = "\x6c\x6f\x63\x61\x6c";`, 1)
	fp, err := parseForcedPage(page)
	if err != nil {
		t.Fatal(err)
	}
	if fp.token != "local" {
		t.Errorf("token = %q, want doChgpwd's own (local)", fp.token)
	}
}

// A vector computed outside Go (OpenSSL and Node's crypto, 2026-11-25) for the
// page's encodeParaValue: AES-256-CBC, key SHA-256("1234567890123456"), IV
// the first 16 bytes of SHA-256("6543210987654321"), zero padding, base64.
func TestEncodeParaValue_KnownVector(t *testing.T) {
	got, err := encodeParaValue("Kergui2026", "1234567890123456", "6543210987654321")
	if err != nil {
		t.Fatal(err)
	}
	if got != "9fvCnBTbjZYX7fVxrMy7fQ==" {
		t.Errorf("encodeParaValue = %q, want 9fvCnBTbjZYX7fVxrMy7fQ==", got)
	}
}

func TestChangePwdForm(t *testing.T) {
	fp := &forcedPassword{token: "tok/+=", key: &mockKey.PublicKey}
	body, err := changePwdForm(fp, "user", "Kergui2026")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(body, "action=changepwd&") {
		t.Errorf("body starts %q, want action=changepwd first", body[:20])
	}
	v, err := url.ParseQuery(body)
	if err != nil {
		t.Fatal(err)
	}
	if v.Get("action") != "changepwd" || v.Get("Username") != "user" || v.Get("_sessionTOKEN") != "tok/+=" {
		t.Errorf("form = %v", v)
	}
	key, iv := openEncode(t, mockKey, v.Get("encode"))
	if len(key) != 16 || len(iv) != 16 {
		t.Errorf("key/iv = %q/%q, want 16 digits each", key, iv)
	}
	for _, f := range []string{"Password", "NewPassword"} {
		got, err := decodeParaValue(v.Get(f), key, iv)
		if err != nil || got != "Kergui2026" {
			t.Errorf("%s decrypts to %q (%v)", f, got, err)
		}
	}
}

func openEncode(t *testing.T, k *rsa.PrivateKey, encode string) (string, string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encode)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := rsa.DecryptPKCS1v15(nil, k, raw)
	if err != nil {
		t.Fatal(err)
	}
	key, iv, ok := strings.Cut(string(plain), "+")
	if !ok {
		t.Fatalf("encode = %q, want key+iv", plain)
	}
	return key, iv
}

// forcedBox serves the captured flow: login, the forced-change page until a
// password is accepted, then the normal interface. reply answers the
// login_changepwd POST.
type forcedBox struct {
	mu      sync.Mutex
	pass    string
	forced  bool
	posts   []url.Values
	reply   func(v url.Values) string
	changed bool
}

func (b *forcedBox) server(t *testing.T) *httptest.Server {
	page := strings.ReplaceAll(forcedPage, forcedPage[strings.Index(forcedPage, "-----BEGIN PUBLIC KEY-----"):strings.Index(forcedPage, "-----END PUBLIC KEY-----")+len("-----END PUBLIC KEY-----")], mockPEMJS())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		q := r.URL.Query()
		typ, tag := q.Get("_type"), q.Get("_tag")
		switch {
		case typ == "loginData" && tag == "login_entry" && r.Method == http.MethodGet:
			w.Write([]byte(`{"sess_token":"t","lockingTime":0}`))
		case typ == "loginData" && tag == "login_token":
			w.Write([]byte(`<ajax_response_xml_root>12345678</ajax_response_xml_root>`))
		case typ == "loginData" && tag == "login_entry":
			_ = r.ParseForm()
			if r.PostForm.Get("Password") != sha256hex(b.pass+"12345678") {
				w.Write([]byte(`{"loginErrMsg":"bad"}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "s", Path: "/"})
			w.Write([]byte(`{"sess_token":"t","login_need_refresh":true}`))
		case typ == "loginData" && tag == "login_changepwd":
			_ = r.ParseForm()
			b.posts = append(b.posts, r.PostForm)
			w.Write([]byte(b.reply(r.PostForm)))
		case b.forced:
			w.Write([]byte(page))
		default:
			w.Write([]byte(mockMainPage))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mockPEMJS() string {
	i := strings.Index(mockMainPage, "-----BEGIN PUBLIC KEY-----")
	j := strings.Index(mockMainPage, "-----END PUBLIC KEY-----") + len("-----END PUBLIC KEY-----")
	return mockMainPage[i:j]
}

// accept changes the password, as the box does, when the form decrypts.
func (b *forcedBox) accept(v url.Values) string {
	key, iv, ok := strings.Cut(func() string {
		raw, _ := base64.StdEncoding.DecodeString(v.Get("encode"))
		p, _ := rsa.DecryptPKCS1v15(nil, mockKey, raw)
		return string(p)
	}(), "+")
	if !ok || v.Get("action") != "changepwd" {
		return `{"login_need_refresh":false,"loginErrMsg":"bad form"}`
	}
	p, err := decodeParaValue(v.Get("Password"), key, iv)
	if err != nil {
		return `{"login_need_refresh":false,"loginErrMsg":"undecryptable"}`
	}
	b.pass, b.forced, b.changed = p, false, true
	return `{"login_need_refresh":true}`
}

func TestGateway_LoginOnForcedPage(t *testing.T) {
	b := &forcedBox{pass: "Label123", forced: true}
	srv := b.server(t)
	gw, _ := New(srv.URL, port.RouterOptions{})
	err := gw.Login(context.Background(), domain.Credentials{Username: "user", Password: "Label123"})
	if !errors.Is(err, domain.ErrPasswordChangeRequired) {
		t.Fatalf("Login = %v, want ErrPasswordChangeRequired", err)
	}
}

func TestGateway_ChangeAdminPassword(t *testing.T) {
	b := &forcedBox{pass: "Label123", forced: true}
	b.reply = b.accept
	srv := b.server(t)
	gw, _ := New(srv.URL, port.RouterOptions{})
	ctx := context.Background()
	if err := gw.ChangeAdminPassword(ctx, domain.Credentials{Username: "user", Password: "Label123"}, "Kergui2026"); err != nil {
		t.Fatalf("ChangeAdminPassword: %v", err)
	}
	if !b.changed || b.pass != "Kergui2026" || len(b.posts) != 1 {
		t.Fatalf("box changed=%v pass=%q posts=%d", b.changed, b.pass, len(b.posts))
	}
	if got := b.posts[0].Get("Username"); got != "user" {
		t.Errorf("Username = %q", got)
	}
	if gw.creds.Password != "Kergui2026" {
		t.Error("the gateway did not log in again with the new password")
	}
}

func TestGateway_ChangeAdminPasswordRefusedByTheBox(t *testing.T) {
	b := &forcedBox{pass: "Label123", forced: true}
	b.reply = func(url.Values) string {
		return `{"login_need_refresh":false,"loginErrMsg":"Le mot de passe est trop faible."}`
	}
	srv := b.server(t)
	gw, _ := New(srv.URL, port.RouterOptions{})
	err := gw.ChangeAdminPassword(context.Background(), domain.Credentials{Username: "user", Password: "Label123"}, "Kergui2026")
	if !errors.Is(err, domain.ErrPasswordRejected) || !strings.Contains(err.Error(), "trop faible") {
		t.Fatalf("err = %v, want ErrPasswordRejected with the box's message", err)
	}
}

func TestGateway_ChangeAdminPasswordWeakNeverReachesTheBox(t *testing.T) {
	b := &forcedBox{pass: "Label123", forced: true}
	b.reply = b.accept
	srv := b.server(t)
	gw, _ := New(srv.URL, port.RouterOptions{})
	err := gw.ChangeAdminPassword(context.Background(), domain.Credentials{Username: "user", Password: "Label123"}, "kergui")
	if !errors.Is(err, domain.ErrWeakPassword) || len(b.posts) != 0 {
		t.Fatalf("err = %v, posts = %d", err, len(b.posts))
	}
}

func TestGateway_ChangeAdminPasswordWhenNotAsked(t *testing.T) {
	b := &forcedBox{pass: "Label123"}
	b.reply = b.accept
	srv := b.server(t)
	gw, _ := New(srv.URL, port.RouterOptions{})
	err := gw.ChangeAdminPassword(context.Background(), domain.Credentials{Username: "user", Password: "Label123"}, "Kergui2026")
	if !errors.Is(err, domain.ErrInvalidInput) || len(b.posts) != 0 {
		t.Fatalf("err = %v, posts = %d", err, len(b.posts))
	}
}
