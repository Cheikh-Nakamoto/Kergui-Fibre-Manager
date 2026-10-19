// Package discovery implements non-destructive router fingerprinting
// (project brief §15). It performs a single unauthenticated GET and classifies
// the router using the markers each registered adapter declares — so it stays
// generic and learns about new models for free.
package discovery

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/httpkit"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

var reTitle = regexp.MustCompile(`(?is)<title>\s*(.*?)\s*</title>`)

// Discoverer fingerprints routers. It reads adapter markers from the factory, so
// it never hardcodes model knowledge.
type Discoverer struct {
	factory port.RouterFactory
	clock   port.Clock
}

var _ port.DiscoveryPort = (*Discoverer)(nil)

// New wires the discoverer.
func New(factory port.RouterFactory, clock port.Clock) *Discoverer {
	return &Discoverer{factory: factory, clock: clock}
}

// Discover performs a single GET on the base URL and classifies the response.
func (d *Discoverer) Discover(ctx context.Context, baseURL string, opts port.RouterOptions) (domain.RouterInfo, error) {
	info := domain.RouterInfo{BaseURL: baseURL, DiscoveredAt: d.clock.Now()}

	status, header, body, err := httpkit.Probe(ctx, baseURL, "/", opts)
	if err != nil {
		return info, fmt.Errorf("%w: %v", domain.ErrRouterUnreachable, err)
	}
	info.Reachable = true
	if header != nil {
		info.HTTPServer = header.Get("Server")
	}
	if m := reTitle.FindSubmatch(body); len(m) == 2 {
		info.Title = strings.TrimSpace(string(m[1]))
	}
	_ = status // retained for future heuristics (auth-required landing pages, etc.)

	hay := strings.ToLower(strings.Join([]string{info.Title, string(body), info.HTTPServer}, " "))

	// Prefer a specific model marker, and among those that match, the longest
	// one. Model names nest — "f660" is a prefix of "f6600p" — so taking the
	// first adapter that matches would hand an F6600P to the F660 adapter
	// purely because of registration order.
	best := domain.AdapterMeta{}
	bestLen := 0
	for _, meta := range d.factory.Available() {
		for _, mk := range meta.Markers {
			if mk == "" || len(mk) <= bestLen {
				continue
			}
			if strings.Contains(hay, strings.ToLower(mk)) {
				best, bestLen = meta, len(mk)
			}
		}
	}
	if bestLen > 0 {
		info.AdapterID = best.ID
		info.Vendor = best.Vendor
		if len(best.Models) > 0 {
			info.Model = best.Models[0]
		}
		info.Confidence = 90
		return info, nil
	}

	// Generic ZTE signals: identify the vendor but ask the user to pick a model.
	for _, sig := range []string{"zxhn", "frm_logintoken", "zte"} {
		if strings.Contains(hay, sig) {
			info.Vendor = "ZTE"
			info.Confidence = 30
			return info, nil
		}
	}

	return info, nil
}
