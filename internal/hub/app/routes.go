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

type routeDeps struct {
	cfg    config.Config
	log    *slog.Logger
	health *health.Checker
	public []registrar // no token: /api/v1/auth/*
	member []registrar // Bearer access token required
}

// Auth endpoint budget per client IP: one request per second, bursts of 10.
const (
	authRate  = 1
	authBurst = 10
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

	limiter := ratelimit.Middleware(ratelimit.New(authRate, authBurst), clientIP(d.cfg.TrustCloudflare))
	authed := middleware.Auth(d.cfg.JWTSecret)(member)

	root := http.NewServeMux()
	root.HandleFunc("GET /health", d.health.LivenessHandler)
	root.HandleFunc("GET /ready", d.health.ReadinessHandler)
	root.Handle("/api/v1/auth/", limiter(public))
	root.Handle("/api/v1/", authed)
	root.Handle("/api/v2/", authed)
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
