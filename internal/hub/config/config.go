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
