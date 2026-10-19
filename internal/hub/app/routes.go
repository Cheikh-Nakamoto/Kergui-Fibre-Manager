package app

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	deliveryhttp "github.com/agile-factory/kernel/delivery/http"
	"github.com/agile-factory/kernel/delivery/http/middleware"
	"github.com/agile-factory/kernel/kernel/health"
	"github.com/agile-factory/kernel/kernel/ratelimit"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/config"
)

// registrar is what every kernel module (and every hub module) exposes.
type registrar interface {
	RegisterRoutes(mux *http.ServeMux)
}

// relayRegistrar is the hub's own addition: the /relay/v1 surface a relay
// calls with its kga_ token instead of a user's JWT.
type relayRegistrar interface {
	RegisterRelayRoutes(mux *http.ServeMux)
}

// webhookRegistrar is the messaging provider's inbound surface. It carries
// no user token: the secret in its path is what authenticates the caller,
// because the provider of the pilot does not sign its calls.
type webhookRegistrar interface {
	RegisterWebhookRoutes(mux *http.ServeMux)
}

type routeDeps struct {
	cfg    config.Config
	log    *slog.Logger
	health *health.Checker
	public []registrar        // no token: /api/v1/auth/*
	member []registrar        // Bearer access token required
	relay  []relayRegistrar   // agent token required, checked by the module
	hooks  []webhookRegistrar // provider webhooks, authenticated by a path secret
}

// Auth endpoint budget per client IP: one request per second, bursts of 10.
const (
	authRate  = 1
	authBurst = 10
	// Pairing budget per client IP. A code is 128 bits, so guessing is
	// hopeless anyway; this is there to keep the route from being used as
	// a free oracle.
	pairRate  = 1
	pairBurst = 5
)

// routes splits the API in two muxes. The kernel's handlers read the caller
// from the request context but do not check the token themselves, so every
// route except /api/v1/auth/* goes through middleware.Auth. The root mux only
// forwards the prefixes listed here: kernel routes registered elsewhere (the
// super-admin POST /admin/v1/tenants) are unreachable on purpose.
func routes(d routeDeps) http.Handler {
	public := http.NewServeMux()
	for _, r := range d.public {
		r.RegisterRoutes(public)
	}
	member := http.NewServeMux()
	for _, r := range d.member {
		r.RegisterRoutes(member)
	}

	// The relay surface authenticates with an agent token, which its own
	// middleware resolves, so it must not go through middleware.Auth. Only
	// pair/redeem is unauthenticated, and it is rate-limited: it is the one
	// route where a caller with no credentials can create something.
	relayMux := http.NewServeMux()
	for _, r := range d.relay {
		r.RegisterRelayRoutes(relayMux)
	}

	hookMux := http.NewServeMux()
	for _, r := range d.hooks {
		r.RegisterWebhookRoutes(hookMux)
	}

	limiter := ratelimit.Middleware(ratelimit.New(authRate, authBurst), clientIP(d.cfg.TrustCloudflare))
	pairLimiter := ratelimit.Middleware(ratelimit.New(pairRate, pairBurst), clientIP(d.cfg.TrustCloudflare))
	authed := middleware.Auth(d.cfg.JWTSecret)(member)

	root := http.NewServeMux()
	root.HandleFunc("GET /health", d.health.LivenessHandler)
	root.HandleFunc("GET /ready", d.health.ReadinessHandler)
	root.Handle("/api/v1/auth/", limiter(public))
	root.Handle("/api/v1/", authed)
	root.Handle("/api/v2/", authed)
	root.Handle("/relay/v1/", relayMux)
	root.Handle("POST /relay/v1/pair/redeem", pairLimiter(relayMux))
	// The inbound webhook is rate-limited like the unauthenticated routes:
	// its path secret is long, but a route that can block a device is not
	// one to leave unmetered.
	root.Handle("/whatsapp/v1/", limiter(hookMux))
	return deliveryhttp.ApplyGlobalMiddlewares(root, d.log)
}

// clientIP keys the rate limiter. The kernel's IPKeyFn trusts the leftmost
// X-Forwarded-For entry, which any client can set; here the only header
// trusted is CF-Connecting-IP, and only when the hub sits behind Cloudflare
// (which overwrites it).
func clientIP(trustCloudflare bool) func(*http.Request) string {
	return func(r *http.Request) string {
		if trustCloudflare {
			if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
				return ip
			}
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	}
}
