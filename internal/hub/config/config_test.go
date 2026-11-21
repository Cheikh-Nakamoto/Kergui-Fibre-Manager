package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func valid() map[string]string {
	return map[string]string{
		"KERGUI_HUB_DATABASE_URL": "postgres://u:p@localhost:5432/hub",
		"KERGUI_HUB_JWT_SECRET":   strings.Repeat("s", 48),
		"KERGUI_HUB_PUBLIC_URL":   "https://hub.example.org/",
	}
}

func TestFromEnv_Defaults(t *testing.T) {
	c, err := FromEnv(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8090" || c.TokenTTL != 15*time.Minute || c.RefreshTTL != 30*24*time.Hour {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.PublicURL != "https://hub.example.org" {
		t.Errorf("trailing slash not trimmed: %q", c.PublicURL)
	}
	if c.SMTP.Host != "" || c.TrustCloudflare {
		t.Errorf("SMTP and Cloudflare must be off by default: %+v", c)
	}
}

func TestFromEnv_RejectsMissingAndWeakSecrets(t *testing.T) {
	_, err := FromEnv(env(map[string]string{"KERGUI_HUB_JWT_SECRET": "short"}))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"KERGUI_HUB_DATABASE_URL", "KERGUI_HUB_JWT_SECRET", "KERGUI_HUB_PUBLIC_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestFromEnv_RejectsBadValues(t *testing.T) {
	m := valid()
	m["KERGUI_HUB_TOKEN_TTL"] = "soon"
	m["KERGUI_HUB_PUBLIC_URL"] = "hub.example.org"
	m["KERGUI_HUB_SMTP_HOST"] = "smtp.example.org"
	m["KERGUI_HUB_SMTP_PORT"] = "99999"
	m["KERGUI_HUB_LOG_LEVEL"] = "loud"
	_, err := FromEnv(env(m))
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"KERGUI_HUB_TOKEN_TTL", "KERGUI_HUB_PUBLIC_URL", "KERGUI_HUB_SMTP_PORT", "KERGUI_HUB_LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestFromEnv_EnvMustBeKnown(t *testing.T) {
	m := valid()
	m["KERGUI_HUB_ENV"] = "staging"
	if _, err := FromEnv(env(m)); err == nil || !strings.Contains(err.Error(), "KERGUI_HUB_ENV") {
		t.Fatalf("want a KERGUI_HUB_ENV error, got %v", err)
	}
	c, err := FromEnv(env(valid()))
	if err != nil || c.Env != "production" {
		t.Fatalf("default env = %q, %v; want production", c.Env, err)
	}
}

const testSeed = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=" // base64 of bytes 0..31

func TestFromEnv_LicensingIsOffByDefault(t *testing.T) {
	c, err := FromEnv(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Licensing.Enabled() || c.Licensing.SweepInterval != 10*time.Minute {
		t.Errorf("licensing defaults: %+v", c.Licensing)
	}
	if c.Licensing.ReturnURL != "https://hub.example.org/licensing/v1/checkout/done" {
		t.Errorf("return URL default: %q", c.Licensing.ReturnURL)
	}
	if k, err := c.Licensing.LoadSigningKey(); err != nil || k != nil {
		t.Errorf("no key configured: %v %v", k, err)
	}
}

func TestFromEnv_LicensingKeyAndNabooPay(t *testing.T) {
	m := valid()
	m["KERGUI_HUB_LICENSING_SIGNING_KEY"] = testSeed
	m["KERGUI_HUB_NABOOPAY_API_KEY"] = "naboo-key"
	m["KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET"] = "naboo-secret"
	m["KERGUI_HUB_LICENSING_SWEEP_INTERVAL"] = "1m"
	c, err := FromEnv(env(m))
	if err != nil {
		t.Fatal(err)
	}
	k, err := c.Licensing.LoadSigningKey()
	if err != nil || len(k) != 64 || !c.Licensing.Enabled() {
		t.Fatalf("key: %v", err)
	}
	if c.Licensing.NabooPayAPIKey != "naboo-key" || c.Licensing.NabooPayWebhookSecret != "naboo-secret" || c.Licensing.SweepInterval != time.Minute {
		t.Errorf("licensing: %+v", c.Licensing)
	}
}

func TestFromEnv_LicensingKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(path, []byte(testSeed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := valid()
	m["KERGUI_HUB_LICENSING_SIGNING_KEY_FILE"] = path
	c, err := FromEnv(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if k, err := c.Licensing.LoadSigningKey(); err != nil || len(k) != 64 {
		t.Fatalf("key file: %v", err)
	}
}

func TestFromEnv_LicensingRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		set  map[string]string
		want string
	}{
		"bad key": {map[string]string{"KERGUI_HUB_LICENSING_SIGNING_KEY": "nope"}, "KERGUI_HUB_LICENSING_SIGNING_KEY"},
		"key and file": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_LICENSING_SIGNING_KEY_FILE": "/run/secrets/k",
		}, "both set"},
		"naboopay without key": {map[string]string{"KERGUI_HUB_NABOOPAY_API_KEY": "k"}, "KERGUI_HUB_NABOOPAY_API_KEY"},
		"webhook without api key": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET": "s",
		}, "KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET"},
		"bad naboopay url": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_NABOOPAY_API_KEY": "k",
			"KERGUI_HUB_NABOOPAY_BASE_URL": "naboo",
		}, "KERGUI_HUB_NABOOPAY_BASE_URL"},
		"bad return url": {map[string]string{"KERGUI_HUB_LICENSING_RETURN_URL": "/done"}, "KERGUI_HUB_LICENSING_RETURN_URL"},
		"bad sweep":      {map[string]string{"KERGUI_HUB_LICENSING_SWEEP_INTERVAL": "often"}, "KERGUI_HUB_LICENSING_SWEEP_INTERVAL"},
	} {
		m := valid()
		for k, v := range tc.set {
			m[k] = v
		}
		_, err := FromEnv(env(m))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error naming %q, got %v", name, tc.want, err)
		}
	}
}

// testServiceAccount is a throwaway Google service account key file.
func testServiceAccount(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "hub@p.iam.gserviceaccount.com",
		"private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	})
	return string(b)
}

func TestFromEnv_PlayIsOffByDefault(t *testing.T) {
	c, err := FromEnv(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Licensing.Play.Enabled() || c.Licensing.Play.WebhookEnabled() {
		t.Errorf("play defaults: %+v", c.Licensing.Play)
	}
	if sa, err := c.Licensing.Play.LoadServiceAccount(); err != nil || sa != nil {
		t.Errorf("no service account configured: %v %v", sa, err)
	}
}

func TestFromEnv_Play(t *testing.T) {
	sa := testServiceAccount(t)
	m := valid()
	m["KERGUI_HUB_LICENSING_SIGNING_KEY"] = testSeed
	m["KERGUI_HUB_PLAY_PACKAGE_NAME"] = "sn.kergui.app"
	m["KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON"] = sa
	m["KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT"] = "rtdn-push@p.iam.gserviceaccount.com"
	c, err := FromEnv(env(m))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Licensing.Play
	if !p.Enabled() || !p.WebhookEnabled() || p.PackageName != "sn.kergui.app" ||
		p.RTDNAudience != "https://hub.example.org/licensing/v1/webhooks/play" {
		t.Fatalf("play %+v", p)
	}
	if got, err := p.LoadServiceAccount(); err != nil || string(got) != sa {
		t.Fatalf("inline service account: %v", err)
	}

	// From a mounted file, with an explicit audience.
	path := filepath.Join(t.TempDir(), "play.json")
	if err := os.WriteFile(path, []byte(sa), 0o600); err != nil {
		t.Fatal(err)
	}
	delete(m, "KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON")
	m["KERGUI_HUB_PLAY_SERVICE_ACCOUNT_FILE"] = path
	m["KERGUI_HUB_PLAY_RTDN_AUDIENCE"] = "kergui-rtdn"
	c, err = FromEnv(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Licensing.Play.LoadServiceAccount(); err != nil || string(got) != sa || c.Licensing.Play.RTDNAudience != "kergui-rtdn" {
		t.Fatalf("file service account: %v %+v", err, c.Licensing.Play)
	}
	// A file that holds no service account fails at start-up.
	_ = os.WriteFile(path, []byte("{}"), 0o600)
	if _, err := c.Licensing.Play.LoadServiceAccount(); err == nil {
		t.Fatal("an empty key file loaded")
	}
}

func TestFromEnv_PlayRefusals(t *testing.T) {
	sa := testServiceAccount(t)
	for name, tc := range map[string]struct {
		set  map[string]string
		want string
	}{
		"package without account": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_PACKAGE_NAME": "sn.kergui.app",
		}, "KERGUI_HUB_PLAY_SERVICE_ACCOUNT"},
		"account without package": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON": sa,
		}, "KERGUI_HUB_PLAY_PACKAGE_NAME"},
		"json and file": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_PACKAGE_NAME": "sn.kergui.app",
			"KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON": sa, "KERGUI_HUB_PLAY_SERVICE_ACCOUNT_FILE": "/run/secrets/play.json",
		}, "both set"},
		"bad account": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_PACKAGE_NAME": "sn.kergui.app",
			"KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON": `{"client_email":"a@b"}`,
		}, "KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON"},
		"bad package": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_PACKAGE_NAME": "kergui app",
			"KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON": sa,
		}, "KERGUI_HUB_PLAY_PACKAGE_NAME"},
		"play without signing key": {map[string]string{
			"KERGUI_HUB_PLAY_PACKAGE_NAME": "sn.kergui.app", "KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON": sa,
		}, "signing key"},
		"rtdn without play": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT": "a@b.iam.gserviceaccount.com",
		}, "KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT"},
		"rtdn not an email": {map[string]string{
			"KERGUI_HUB_LICENSING_SIGNING_KEY": testSeed, "KERGUI_HUB_PLAY_PACKAGE_NAME": "sn.kergui.app",
			"KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON": sa, "KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT": "pubsub",
		}, "KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT"},
	} {
		m := valid()
		for k, v := range tc.set {
			m[k] = v
		}
		_, err := FromEnv(env(m))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want an error naming %q, got %v", name, tc.want, err)
		}
	}
}
