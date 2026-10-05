// Package usecase holds the application business rules (interactors). Each
// interactor orchestrates ports to fulfil one user intent. It depends only on
// the domain and on port interfaces — never on a concrete adapter.
package usecase

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// RouterID derives a stable id from a base URL, e.g.
// "http://192.168.1.1" -> "192-168-1-1".
func RouterID(baseURL string) string {
	s := strings.ToLower(strings.TrimSpace(baseURL))
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	s = nonSlug.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "router"
	}
	return s
}

// DeviceID is the deterministic inventory id of a device on a router.
func DeviceID(routerID string, mac domain.MAC) string {
	return routerID + "|" + mac.String()
}

// AccessRuleID is the deterministic id of an access rule on a router.
func AccessRuleID(routerID string, mac domain.MAC) string {
	return routerID + "|acl|" + mac.String()
}

// resolveAdapterID returns the adapter id to use for a router. If adapterID is
// empty it runs non-destructive discovery and uses the suggested adapter.
func resolveAdapterID(ctx context.Context, disco port.DiscoveryPort, baseURL, adapterID string, opts port.RouterOptions) (string, *domain.RouterInfo, error) {
	if adapterID != "" {
		return adapterID, nil, nil
	}
	info, err := disco.Discover(ctx, baseURL, opts)
	if err != nil {
		return "", nil, err
	}
	if info.AdapterID == "" {
		return "", &info, fmt.Errorf("%w: could not identify the router at %s (pass --adapter)", domain.ErrUnsupportedModel, baseURL)
	}
	return info.AdapterID, &info, nil
}
