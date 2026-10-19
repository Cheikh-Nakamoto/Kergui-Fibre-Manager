// Package config reads the hub's settings from the environment. The hub does
// not use the kernel's config.Load: that one reads .env files from the working
// directory (the repository root holds kergui's own secrets) and falls back to
// a well-known JWT secret. Here every secret is required and checked.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// minJWTSecretLen is the shortest HS256 secret accepted (bytes).
const minJWTSecretLen = 32

// The messaging providers of RFC-005 H2.
const (
	// NotifierConsole logs what it would send. It is the default, and what
	// CI and the pilot use before a WhatsApp number exists.
	NotifierConsole = "console"
	// NotifierEvolution is a self-hosted Evolution Go instance.
	NotifierEvolution = "evolution"
)

// minWebhookSecretLen is the shortest inbound webhook secret accepted. The
// secret travels in the URL path because Evolution Go does not sign its
// calls, so it has to be long enough that the path cannot be guessed.
const minWebhookSecretLen = 24

// Config holds everything cmd/kergui-hub needs to start.
type Config struct {
	Env             string        // "production" (default) or "development"
	Addr            string        // listen address, e.g. ":8090"
	DatabaseURL     string        // postgres://…
	JWTSecret       string        // HS256 secret for kernel access tokens
	TokenTTL        time.Duration // access token lifetime
	RefreshTTL      time.Duration // refresh token lifetime
	PublicURL       string        // public base URL, used in emailed links
	MailFrom        string        // From: header of emails
	SMTP            SMTP          // empty Host = log emails instead of sending
	TrustCloudflare bool          // take the client IP from CF-Connecting-IP
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	WhatsApp        WhatsApp // alerts channel; Provider "console" logs instead of sending
}

// WhatsApp configures the alerts channel (RFC-005 H2).
type WhatsApp struct {
	// Provider is "console" (the pilot and CI: messages are logged) or
	// "evolution" (a self-hosted Evolution Go instance).
	Provider string
	// Number is the household-facing Kergui number in E.164. The wa.me
	// opt-in link points at it, so without it no number can be linked.
	Number string
	// WebhookSecret authenticates the provider's inbound calls. Evolution
	// Go does not sign its webhooks, so the secret travels in the path and
	// is compared in constant time; empty turns the inbound route off,
	// which also turns "reply 1 to block" off.
	WebhookSecret string
	// Evolution is read only when Provider is "evolution".
	Evolution Evolution
}

// Evolution points at a self-hosted Evolution Go instance.
type Evolution struct {
	BaseURL  string
	Instance string
	APIKey   string
}

// SMTP configures outgoing email. Host empty means "no SMTP".
type SMTP struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      bool // implicit TLS (465); false = STARTTLS
}

// FromEnv builds a Config from getenv (os.Getenv in production).
func FromEnv(getenv func(string) string) (Config, error) {
	e := reader{getenv: getenv}
	c := Config{
		Env:             e.str("KERGUI_HUB_ENV", "production"),
		Addr:            e.str("KERGUI_HUB_ADDR", ":8090"),
		DatabaseURL:     e.str("KERGUI_HUB_DATABASE_URL", ""),
		JWTSecret:       getenv("KERGUI_HUB_JWT_SECRET"), // not trimmed: it is opaque
		TokenTTL:        e.duration("KERGUI_HUB_TOKEN_TTL", 15*time.Minute),
		RefreshTTL:      e.duration("KERGUI_HUB_REFRESH_TTL", 30*24*time.Hour),
		PublicURL:       strings.TrimRight(e.str("KERGUI_HUB_PUBLIC_URL", ""), "/"),
		MailFrom:        e.str("KERGUI_HUB_MAIL_FROM", "Kergui <no-reply@kergui.local>"),
		TrustCloudflare: e.boolean("KERGUI_HUB_TRUST_CLOUDFLARE"),
		WhatsApp: WhatsApp{
			Provider:      strings.ToLower(e.str("KERGUI_HUB_NOTIFIER", NotifierConsole)),
			Number:        e.str("KERGUI_HUB_WHATSAPP_NUMBER", ""),
			WebhookSecret: getenv("KERGUI_HUB_WEBHOOK_SECRET"), // opaque, not trimmed
			Evolution: Evolution{
				BaseURL:  e.str("KERGUI_HUB_EVOLUTION_URL", ""),
				Instance: e.str("KERGUI_HUB_EVOLUTION_INSTANCE", ""),
				APIKey:   getenv("KERGUI_HUB_EVOLUTION_APIKEY"), // opaque, not trimmed
			},
		},
		ShutdownTimeout: e.duration("KERGUI_HUB_SHUTDOWN_TIMEOUT", 10*time.Second),
		SMTP: SMTP{
			Host:     e.str("KERGUI_HUB_SMTP_HOST", ""),
			Username: e.str("KERGUI_HUB_SMTP_USER", ""),
			Password: getenv("KERGUI_HUB_SMTP_PASSWORD"),
			TLS:      e.boolean("KERGUI_HUB_SMTP_TLS"),
		},
	}
	if c.SMTP.Host != "" {
		c.SMTP.Port = e.port("KERGUI_HUB_SMTP_PORT", "587")
	}
	if err := c.LogLevel.UnmarshalText([]byte(e.str("KERGUI_HUB_LOG_LEVEL", "info"))); err != nil {
		e.errs = append(e.errs, fmt.Errorf("KERGUI_HUB_LOG_LEVEL: %w", err))
	}
	return c, errors.Join(append(e.errs, c.validate()...)...)
}

// validate checks the values that have no safe default.
func (c Config) validate() []error {
	var errs []error
	if c.Env != "production" && c.Env != "development" {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_ENV: %q is neither production nor development", c.Env))
	}
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("KERGUI_HUB_DATABASE_URL is required"))
	}
	if len(c.JWTSecret) < minJWTSecretLen {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_JWT_SECRET must be at least %d bytes (try: openssl rand -base64 48)", minJWTSecretLen))
	}
	if c.PublicURL == "" {
		errs = append(errs, errors.New("KERGUI_HUB_PUBLIC_URL is required (e.g. https://hub.example.org)"))
	} else if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_PUBLIC_URL: invalid URL %q", c.PublicURL))
	}
	return append(errs, c.WhatsApp.validate()...)
}

// validate checks the alerts channel. A hub with no WhatsApp at all is a
// valid hub — it alerts through the console log and the app — so these
// errors only fire for settings that are half-filled, which would fail
// later, at the worst moment: when a household's alert goes out.
func (w WhatsApp) validate() []error {
	var errs []error
	switch w.Provider {
	case NotifierConsole:
	case NotifierEvolution:
		if w.Evolution.BaseURL == "" {
			errs = append(errs, errors.New("KERGUI_HUB_EVOLUTION_URL is required with KERGUI_HUB_NOTIFIER=evolution"))
		}
		if w.Evolution.Instance == "" {
			errs = append(errs, errors.New("KERGUI_HUB_EVOLUTION_INSTANCE is required with KERGUI_HUB_NOTIFIER=evolution"))
		}
		if w.Evolution.APIKey == "" {
			errs = append(errs, errors.New("KERGUI_HUB_EVOLUTION_APIKEY is required with KERGUI_HUB_NOTIFIER=evolution"))
		}
		if w.Number == "" {
			errs = append(errs, errors.New("KERGUI_HUB_WHATSAPP_NUMBER is required with KERGUI_HUB_NOTIFIER=evolution"))
		}
	default:
		errs = append(errs, fmt.Errorf("KERGUI_HUB_NOTIFIER: %q is neither %s nor %s",
			w.Provider, NotifierConsole, NotifierEvolution))
	}
	if s := w.WebhookSecret; s != "" && len(s) < minWebhookSecretLen {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_WEBHOOK_SECRET must be at least %d characters (try: openssl rand -hex 24)", minWebhookSecretLen))
	}
	return errs
}

// reader parses typed values and collects every error instead of stopping
// at the first one, so a misconfigured deployment is fixed in one pass.
type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) str(key, def string) string {
	if v := strings.TrimSpace(r.getenv(key)); v != "" {
		return v
	}
	return def
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	raw := r.str(key, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid duration %q", key, raw))
		return def
	}
	return d
}

func (r *reader) boolean(key string) bool {
	raw := r.str(key, "false")
	b, err := strconv.ParseBool(raw)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid boolean %q", key, raw))
	}
	return b
}

func (r *reader) port(key, def string) int {
	p, err := strconv.Atoi(r.str(key, def))
	if err != nil || p <= 0 || p > 65535 {
		r.errs = append(r.errs, fmt.Errorf("%s: invalid port", key))
		return 0
	}
	return p
}
