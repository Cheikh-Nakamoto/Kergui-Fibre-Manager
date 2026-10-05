package usecase

import (
	"context"
	"fmt"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ListDevices performs the read-only device listing (brief §5) and reconciles the
// result with the local inventory: it fills vendor/timestamps, preserves the
// user's custom name, and flags devices never seen before (brief §7, §17). This
// reconcile step is the Milestone-1 realisation of "Synchronize".
type ListDevices struct {
	factory port.RouterFactory
	vault   port.CredentialVault
	devices port.DeviceRepository
	vendors port.VendorLookup
	disco   port.DiscoveryPort
	clock   port.Clock
	log     port.Logger
}

// NewListDevices wires the interactor.
func NewListDevices(f port.RouterFactory, v port.CredentialVault, dr port.DeviceRepository, vl port.VendorLookup, d port.DiscoveryPort, c port.Clock, l port.Logger) *ListDevices {
	return &ListDevices{factory: f, vault: v, devices: dr, vendors: vl, disco: d, clock: c, log: l}
}

// ListDevicesInput parameterises the listing.
type ListDevicesInput struct {
	BaseURL   string
	AdapterID string
	Opts      port.RouterOptions
	// Creds, when a password is set, override the stored credentials.
	Creds domain.Credentials
	// Persist reconciles the result into the local inventory and flags new MACs.
	Persist bool
}

// Execute logs in, fetches devices and returns them as views.
func (uc *ListDevices) Execute(ctx context.Context, in ListDevicesInput) ([]port.DeviceView, error) {
	adapterID, _, err := resolveAdapterID(ctx, uc.disco, in.BaseURL, in.AdapterID, in.Opts)
	if err != nil {
		return nil, err
	}
	routerID := RouterID(in.BaseURL)

	creds, err := loadCreds(ctx, uc.vault, in.BaseURL, in.Creds)
	if err != nil {
		return nil, err
	}

	gw, err := uc.factory.New(adapterID, in.BaseURL, in.Opts)
	if err != nil {
		return nil, err
	}
	if err := gw.Login(ctx, creds); err != nil {
		return nil, err
	}
	devs, err := gw.Devices(ctx)
	if err != nil {
		return nil, err
	}

	now := uc.clock.Now()
	views := make([]port.DeviceView, 0, len(devs))
	for _, d := range devs {
		d.RouterID = routerID
		d.ID = DeviceID(routerID, d.MAC)
		if d.Vendor == "" && uc.vendors != nil {
			d.Vendor = uc.vendors.Vendor(d.MAC)
		}
		if d.LastSeen.IsZero() {
			d.LastSeen = now
		}

		newly := false
		if in.Persist && uc.devices != nil {
			if existing, ok, err := uc.devices.FindByMAC(ctx, routerID, d.MAC); err == nil && ok {
				if !existing.FirstSeen.IsZero() {
					d.FirstSeen = existing.FirstSeen
				}
				if d.CustomName == "" {
					d.CustomName = existing.CustomName // never clobber the user's label
				}
				if d.Notes == "" {
					d.Notes = existing.Notes
				}
			} else if d.FirstSeen.IsZero() {
				d.FirstSeen = now
			}
			created, err := uc.devices.Upsert(ctx, d)
			if err != nil {
				return nil, fmt.Errorf("persist device %s: %w", d.MAC, err)
			}
			newly = created
		} else if d.FirstSeen.IsZero() {
			d.FirstSeen = now
		}

		views = append(views, port.DeviceView{Device: d, NewlySeen: newly})
	}
	return views, nil
}
