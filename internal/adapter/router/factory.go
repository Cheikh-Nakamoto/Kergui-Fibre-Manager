// Package router holds the modular router-adapter registry. It implements the
// application's port.RouterFactory without importing any concrete adapter, so the
// core never depends on a specific model. Adapters register themselves via the
// catalog; adding a model touches only the catalog, never the core.
package router

import (
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// BuildFunc constructs a gateway bound to a base URL.
type BuildFunc func(baseURL string, opts port.RouterOptions) (port.RouterPort, error)

type entry struct {
	meta  domain.AdapterMeta
	build BuildFunc
}

// Factory is a registry of router adapters.
type Factory struct {
	entries map[string]entry
	order   []string
}

var _ port.RouterFactory = (*Factory)(nil)

// NewFactory returns an empty registry.
func NewFactory() *Factory { return &Factory{entries: map[string]entry{}} }

// Register adds (or replaces) an adapter by its metadata id.
func (f *Factory) Register(meta domain.AdapterMeta, build BuildFunc) {
	if _, exists := f.entries[meta.ID]; !exists {
		f.order = append(f.order, meta.ID)
	}
	f.entries[meta.ID] = entry{meta: meta, build: build}
}

// New builds a gateway for a registered adapter id.
func (f *Factory) New(adapterID, baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
	e, ok := f.entries[adapterID]
	if !ok {
		return nil, fmt.Errorf("%w: adapter %q is not registered", domain.ErrUnsupportedModel, adapterID)
	}
	return e.build(baseURL, opts)
}

// Available lists registered adapters in registration order.
func (f *Factory) Available() []domain.AdapterMeta {
	out := make([]domain.AdapterMeta, 0, len(f.order))
	for _, id := range f.order {
		out = append(out, f.entries[id].meta)
	}
	return out
}

// Lookup returns one adapter's metadata.
func (f *Factory) Lookup(id string) (domain.AdapterMeta, bool) {
	e, ok := f.entries[id]
	if !ok {
		return domain.AdapterMeta{}, false
	}
	return e.meta, true
}
