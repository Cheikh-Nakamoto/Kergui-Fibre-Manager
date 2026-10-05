package usecase

import (
	"context"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// DiscoverRouter performs non-destructive discovery of a router (brief §15, §22).
type DiscoverRouter struct {
	disco port.DiscoveryPort
}

// NewDiscoverRouter wires the interactor.
func NewDiscoverRouter(disco port.DiscoveryPort) *DiscoverRouter {
	return &DiscoverRouter{disco: disco}
}

// DiscoverInput parameterises discovery.
type DiscoverInput struct {
	BaseURL string
	Opts    port.RouterOptions
}

// Execute fingerprints the router and returns what could be learned without
// authenticating or modifying anything.
func (uc *DiscoverRouter) Execute(ctx context.Context, in DiscoverInput) (domain.RouterInfo, error) {
	return uc.disco.Discover(ctx, in.BaseURL, in.Opts)
}
