// Package vendor resolves a MAC OUI to a vendor name. Milestone 1 ships an empty
// table (returns ""); a full IEEE OUI database can be loaded later without
// touching any other layer — it only needs to satisfy port.VendorLookup.
package vendor

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Table maps an OUI ("aa:bb:cc") to a vendor name.
type Table struct{ ouis map[string]string }

var _ port.VendorLookup = (*Table)(nil)

// New returns a lookup. Pass nil for an empty table.
func New(ouis map[string]string) *Table {
	if ouis == nil {
		ouis = map[string]string{}
	}
	return &Table{ouis: ouis}
}

// Vendor returns the vendor for a MAC, or "" when unknown.
func (t *Table) Vendor(mac domain.MAC) string { return t.ouis[mac.OUI()] }
