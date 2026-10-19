package config

import (
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
