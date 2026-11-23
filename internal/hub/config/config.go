// Package config reads the hub's settings from the environment. The hub does
// not use the kernel's config.Load: that one reads .env files from the working
// directory (the repository root holds kergui's own secrets) and falls back to
// a well-known JWT secret. Here every secret is required and checked.
package config

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/hubkey"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/googleplay"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/signingkey"
)

// minJWTSecretLen is the shortest HS256 secret accepted (bytes).
const minJWTSecretLen = 32

// The messaging providers of RFC-005 H2.
const (
	// NotifierConsole logs what it would send. It is the default, and what
	// CI and the pilot use before a WhatsApp number exists.
	NotifierConsole = "console"
	// NotifierWhatsmeow is a WhatsApp Web session run inside the hub
	// (go.mau.fi/whatsmeow, issue #59), linked from the admin routes.
	NotifierWhatsmeow = "whatsmeow"
)

// The app push channels (issue #49).
const (
	// PushConsole logs the notifications it would push: the default, so a
	// hub without an Expo project still runs and shows what it would send.
	PushConsole = "console"
	// PushExpo hands them to Expo's push service (FCM / APNs).
	PushExpo = "expo"
)

// minWebhookSecretLen is the shortest inbound webhook secret accepted. The
// secret travels in the URL path (the console provider's webhook is not
// signed), so it has to be long enough that the path cannot be guessed.
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
	WhatsApp        WhatsApp  // alerts channel; Provider "console" logs instead of sending
	Push            Push      // app push channel; Provider "console" logs instead of pushing
	Licensing       Licensing // RFC-006 accounts and passes; off without a signing key
}

// Licensing configures the Pro passes (RFC-006). Without a signing key the
// module is off: the hub cannot issue the tokens a pass is worth, so it does
// not sell any.
type Licensing struct {
	// SigningKey and SigningKeyFile hold the Ed25519 key that signs the
	// entitlement tokens, inline or as a path to a mounted secret, never
	// both. PEM (openssl genpkey -algorithm ed25519) or a base64/hex seed.
	SigningKey     string
	SigningKeyFile string
	// NextPublicKeys, RetiringPublicKeys and RevokedKeyIDs drive a signing
	// key rotation (docs/licensing-key-rotation.md): public keys in base64
	// published beside the active one, before it signs (next) and after
	// (retiring), and the key ids no token is honoured from any more. Each
	// is a list separated by commas or spaces.
	NextPublicKeys     string
	RetiringPublicKeys string
	RevokedKeyIDs      string
	// NabooPay sells the passes of the direct-APK flavor. An empty API key
	// turns checkouts off; the webhook secret authenticates NabooPay's
	// calls (X-Signature), and without it only the sweep settles orders.
	NabooPayAPIKey        string
	NabooPayBaseURL       string
	NabooPayWebhookSecret string
	// ReturnURL is where NabooPay sends the payer back. It defaults to the
	// hub's own "you can go back to the app" page.
	ReturnURL string
	// SweepInterval is how often expiry reminders go out and pending
	// orders are re-checked with NabooPay.
	SweepInterval time.Duration
	// Play sells the subscriptions of the Google Play flavor.
	Play Play
}

// Play configures Google Play Billing (RFC-006, Two channels). It is off
// without a package name; the /licensing/v1/play routes then answer 404.
type Play struct {
	// PackageName is the app's application id on Google Play.
	PackageName string
	// ServiceAccountJSON and ServiceAccountFile hold the Google service
	// account key the hub calls the Play Developer API with, inline or as a
	// path to a mounted secret, never both.
	ServiceAccountJSON string
	ServiceAccountFile string
	// RTDNServiceAccount is the email the Pub/Sub push subscription signs
	// its OIDC token as. Empty turns the notification webhook off.
	RTDNServiceAccount string
	// RTDNAudience is the push subscription's audience; it defaults to the
	// webhook's URL, which is Pub/Sub's own default.
	RTDNAudience string
}

// Enabled reports whether a Play package is configured.
func (p Play) Enabled() bool { return p.PackageName != "" }

// WebhookEnabled reports whether real-time notifications are accepted.
func (p Play) WebhookEnabled() bool { return p.Enabled() && p.RTDNServiceAccount != "" }

// LoadServiceAccount returns the key file's JSON, read from the file if
// that is where it is, and checks it. It is nil when Play is off.
func (p Play) LoadServiceAccount() ([]byte, error) {
	raw := []byte(p.ServiceAccountJSON)
	if p.ServiceAccountFile != "" {
		b, err := os.ReadFile(p.ServiceAccountFile)
		if err != nil {
			return nil, fmt.Errorf("KERGUI_HUB_PLAY_SERVICE_ACCOUNT_FILE: %w", err)
		}
		raw = b
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if _, err := googleplay.ParseServiceAccount(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Enabled reports whether a signing key is configured.
func (l Licensing) Enabled() bool { return l.SigningKey != "" || l.SigningKeyFile != "" }

// LoadSigningKey reads the key, from the file if that is where it is. It is
// nil when licensing is off.
func (l Licensing) LoadSigningKey() (ed25519.PrivateKey, error) {
	return signingkey.Load(l.SigningKey, l.SigningKeyFile)
}

// LoadRotation reads the keys published beside the active one and the
// revoked key ids, checked against the active key.
func (l Licensing) LoadRotation(active ed25519.PrivateKey) (signingkey.Rotation, error) {
	if len(active) != ed25519.PrivateKeySize {
		return signingkey.Rotation{}, errors.New("licensing: a rotation needs the active signing key")
	}
	r, err := signingkey.ParseRotation(active.Public().(ed25519.PublicKey), l.NextPublicKeys, l.RetiringPublicKeys, l.RevokedKeyIDs)
	if err != nil {
		return signingkey.Rotation{}, fmt.Errorf("KERGUI_HUB_LICENSING_{NEXT,RETIRING}_PUBLIC_KEYS / _REVOKED_KEY_IDS: %w", err)
	}
	return r, nil
}

// WhatsApp configures the alerts channel (RFC-005 H2).
type WhatsApp struct {
	// Provider is "console" (the pilot and CI: messages are logged) or
	// "whatsmeow" (a WhatsApp Web session inside the hub, issue #59).
	Provider string
	// Number is the household-facing Kergui number in E.164. The wa.me
	// opt-in link points at it, so without it no number can be linked.
	Number string
	// WebhookSecret authenticates the console provider's inbound calls
	// (tests and pilots): the secret travels in the path and is compared in
	// constant time; empty turns that route off. whatsmeow needs none: its
	// replies arrive inside the hub.
	WebhookSecret string
	// KeySeed derives the hub's narrow block key, one per household
	// (internal/hub/hubkey). Empty turns "reply 1 to block" off, which is
	// a valid way to run a hub.
	KeySeed string
}

// Push configures app notifications (issue #49).
type Push struct {
	// Provider is "console" (default: logged) or "expo".
	Provider string
	// ExpoAccessToken is the EAS project's push access token, required by
	// Expo only when "enhanced security for push notifications" is on.
	ExpoAccessToken string
	// ExpoURL overrides Expo's push service (https://exp.host), for a test
	// or a proxy.
	ExpoURL string
}

// validate checks the push channel.
func (p Push) validate() []error {
	switch p.Provider {
	case PushConsole:
		return nil
	case PushExpo:
		if p.ExpoURL != "" {
			if u, err := url.Parse(p.ExpoURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return []error{fmt.Errorf("KERGUI_HUB_EXPO_PUSH_URL: invalid URL %q", p.ExpoURL)}
			}
		}
		return nil
	default:
		return []error{fmt.Errorf("KERGUI_HUB_PUSH_PROVIDER: %q is neither %s nor %s", p.Provider, PushConsole, PushExpo)}
	}
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
			KeySeed:       getenv("KERGUI_HUB_KEY_SEED"),       // opaque, not trimmed
		},
		Push: Push{
			Provider:        strings.ToLower(e.str("KERGUI_HUB_PUSH_PROVIDER", PushConsole)),
			ExpoAccessToken: getenv("KERGUI_HUB_EXPO_ACCESS_TOKEN"), // opaque, not trimmed
			ExpoURL:         strings.TrimRight(e.str("KERGUI_HUB_EXPO_PUSH_URL", ""), "/"),
		},
		Licensing: Licensing{
			SigningKey:            getenv("KERGUI_HUB_LICENSING_SIGNING_KEY"), // opaque, not trimmed
			SigningKeyFile:        e.str("KERGUI_HUB_LICENSING_SIGNING_KEY_FILE", ""),
			NextPublicKeys:        e.str("KERGUI_HUB_LICENSING_NEXT_PUBLIC_KEYS", ""),
			RetiringPublicKeys:    e.str("KERGUI_HUB_LICENSING_RETIRING_PUBLIC_KEYS", ""),
			RevokedKeyIDs:         e.str("KERGUI_HUB_LICENSING_REVOKED_KEY_IDS", ""),
			NabooPayAPIKey:        getenv("KERGUI_HUB_NABOOPAY_API_KEY"), // opaque, not trimmed
			NabooPayBaseURL:       e.str("KERGUI_HUB_NABOOPAY_BASE_URL", ""),
			NabooPayWebhookSecret: getenv("KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET"), // opaque, not trimmed
			ReturnURL:             e.str("KERGUI_HUB_LICENSING_RETURN_URL", ""),
			SweepInterval:         e.duration("KERGUI_HUB_LICENSING_SWEEP_INTERVAL", 10*time.Minute),
			Play: Play{
				PackageName:        e.str("KERGUI_HUB_PLAY_PACKAGE_NAME", ""),
				ServiceAccountJSON: strings.TrimSpace(getenv("KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON")),
				ServiceAccountFile: e.str("KERGUI_HUB_PLAY_SERVICE_ACCOUNT_FILE", ""),
				RTDNServiceAccount: e.str("KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT", ""),
				RTDNAudience:       e.str("KERGUI_HUB_PLAY_RTDN_AUDIENCE", ""),
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
	if c.Licensing.ReturnURL == "" && c.PublicURL != "" {
		c.Licensing.ReturnURL = c.PublicURL + "/licensing/v1/checkout/done"
	}
	if c.Licensing.Play.RTDNAudience == "" && c.PublicURL != "" {
		c.Licensing.Play.RTDNAudience = c.PublicURL + "/licensing/v1/webhooks/play"
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
	errs = append(errs, c.WhatsApp.validate()...)
	errs = append(errs, c.Push.validate()...)
	return append(errs, c.Licensing.validate()...)
}

// validate checks the licensing settings. The key file is read at start-up,
// not here, so a secret mounted after the config is parsed still works.
func (l Licensing) validate() []error {
	var errs []error
	switch {
	case l.SigningKey != "" && l.SigningKeyFile != "":
		errs = append(errs, errors.New("KERGUI_HUB_LICENSING_SIGNING_KEY and KERGUI_HUB_LICENSING_SIGNING_KEY_FILE are both set: keep one"))
	case l.SigningKey != "":
		key, err := signingkey.Parse([]byte(l.SigningKey))
		if err != nil {
			errs = append(errs, fmt.Errorf("KERGUI_HUB_LICENSING_SIGNING_KEY: %w (try: openssl genpkey -algorithm ed25519)", err))
		} else if _, err := l.LoadRotation(key); err != nil {
			errs = append(errs, err)
		}
	}
	if !l.Enabled() && (l.NextPublicKeys != "" || l.RetiringPublicKeys != "" || l.RevokedKeyIDs != "") {
		errs = append(errs, errors.New("KERGUI_HUB_LICENSING_{NEXT,RETIRING}_PUBLIC_KEYS / _REVOKED_KEY_IDS are set but no licensing signing key: there is no key to rotate"))
	}
	if l.NabooPayAPIKey != "" && !l.Enabled() {
		errs = append(errs, errors.New("KERGUI_HUB_NABOOPAY_API_KEY is set but no licensing signing key: a paid pass could never be turned into a token"))
	}
	if l.NabooPayWebhookSecret != "" && l.NabooPayAPIKey == "" {
		errs = append(errs, errors.New("KERGUI_HUB_NABOOPAY_WEBHOOK_SECRET is set but KERGUI_HUB_NABOOPAY_API_KEY is not: an order's status could never be checked"))
	}
	if u := l.NabooPayBaseURL; u != "" {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" && p.Scheme != "http" || p.Host == "" {
			errs = append(errs, fmt.Errorf("KERGUI_HUB_NABOOPAY_BASE_URL: invalid URL %q", u))
		}
	}
	if u := l.ReturnURL; u != "" {
		if p, err := url.Parse(u); err != nil || p.Scheme != "https" && p.Scheme != "http" || p.Host == "" {
			errs = append(errs, fmt.Errorf("KERGUI_HUB_LICENSING_RETURN_URL: invalid URL %q", u))
		}
	}
	return append(errs, l.Play.validate(l.Enabled())...)
}

// packageName is an Android application id: dot-separated Java identifiers.
var packageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)

// validate checks the Play settings. The key file is read at start-up.
func (p Play) validate(signing bool) []error {
	var errs []error
	hasAccount := p.ServiceAccountJSON != "" || p.ServiceAccountFile != ""
	switch {
	case p.ServiceAccountJSON != "" && p.ServiceAccountFile != "":
		errs = append(errs, errors.New("KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON and KERGUI_HUB_PLAY_SERVICE_ACCOUNT_FILE are both set: keep one"))
	case p.ServiceAccountJSON != "":
		if _, err := googleplay.ParseServiceAccount([]byte(p.ServiceAccountJSON)); err != nil {
			errs = append(errs, fmt.Errorf("KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON: %w", err))
		}
	}
	switch {
	case p.Enabled() && !hasAccount:
		errs = append(errs, errors.New("KERGUI_HUB_PLAY_PACKAGE_NAME is set but no KERGUI_HUB_PLAY_SERVICE_ACCOUNT_JSON or _FILE: purchases could never be verified"))
	case !p.Enabled() && hasAccount:
		errs = append(errs, errors.New("a Play service account is set but KERGUI_HUB_PLAY_PACKAGE_NAME is not"))
	}
	if p.Enabled() && !packageName.MatchString(p.PackageName) {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_PLAY_PACKAGE_NAME: %q is not an Android application id", p.PackageName))
	}
	if p.Enabled() && !signing {
		errs = append(errs, errors.New("KERGUI_HUB_PLAY_PACKAGE_NAME is set but no licensing signing key: a Play purchase could never be turned into a token"))
	}
	if r := p.RTDNServiceAccount; r != "" {
		if !p.Enabled() {
			errs = append(errs, errors.New("KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT is set but KERGUI_HUB_PLAY_PACKAGE_NAME is not"))
		}
		if at := strings.LastIndex(r, "@"); at <= 0 || at == len(r)-1 {
			errs = append(errs, fmt.Errorf("KERGUI_HUB_PLAY_RTDN_SERVICE_ACCOUNT: %q is not a service account email", r))
		}
	}
	return errs
}

// validate checks the alerts channel. A hub with no WhatsApp at all is a
// valid hub — it alerts through the console log and the app — so these
// errors only fire for settings that are half-filled, which would fail
// later, at the worst moment: when a household's alert goes out.
func (w WhatsApp) validate() []error {
	var errs []error
	switch w.Provider {
	case NotifierConsole:
	case NotifierWhatsmeow:
		if w.Number == "" {
			errs = append(errs, errors.New("KERGUI_HUB_WHATSAPP_NUMBER is required with KERGUI_HUB_NOTIFIER=whatsmeow"))
		}
	default:
		errs = append(errs, fmt.Errorf("KERGUI_HUB_NOTIFIER: %q is neither %s nor %s",
			w.Provider, NotifierConsole, NotifierWhatsmeow))
	}
	if s := w.WebhookSecret; s != "" && len(s) < minWebhookSecretLen {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_WEBHOOK_SECRET must be at least %d characters (try: openssl rand -hex 24)", minWebhookSecretLen))
	}
	if s := w.KeySeed; s != "" && len(s) < hubkey.MinSeedLen {
		errs = append(errs, fmt.Errorf("KERGUI_HUB_KEY_SEED must be at least %d bytes (try: openssl rand -hex 32)", hubkey.MinSeedLen))
	}
	// A hub that can send alerts but not act on the answers would promise
	// "reply 1 to block" in every message and then refuse: say so at
	// start-up instead.
	// The whatsmeow session receives the replies itself, without a webhook.
	if w.KeySeed != "" && w.WebhookSecret == "" && w.Provider != NotifierWhatsmeow {
		errs = append(errs, errors.New("KERGUI_HUB_KEY_SEED is set but KERGUI_HUB_WEBHOOK_SECRET is not: replies could never reach the hub"))
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
