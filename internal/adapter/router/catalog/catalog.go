// Package catalog is the one place that knows the full set of built-in router
// adapters. It wires them into a factory. Adding a new model means adding its
// package and one Register line here — the domain and use-case layers never
// change.
package catalog

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/funbox"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef660"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef680"
)

// New returns a factory with every built-in adapter registered.
func New() *router.Factory {
	f := router.NewFactory()
	ztef660.Register(f) // reference adapter (read path implemented)
	ztef680.Register(f) // skeleton
	funbox.Register(f)  // skeleton
	return f
}
