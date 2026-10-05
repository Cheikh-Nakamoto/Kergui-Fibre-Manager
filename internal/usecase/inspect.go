package usecase

import (
	"context"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// InspectRouter produces the diagnostic report the brief asks for (§4): what the
// router is, how authentication works, and which endpoints each operation maps
// to — always flagging that endpoints are UNVERIFIED until confirmed on real
// hardware. It is read-only and never runs a write operation.
type InspectRouter struct {
	factory port.RouterFactory
	disco   port.DiscoveryPort
	vault   port.CredentialVault
	clock   port.Clock
	log     port.Logger
}

// NewInspectRouter wires the interactor.
func NewInspectRouter(f port.RouterFactory, d port.DiscoveryPort, v port.CredentialVault, c port.Clock, l port.Logger) *InspectRouter {
	return &InspectRouter{factory: f, disco: d, vault: v, clock: c, log: l}
}

// InspectInput parameterises the inspection.
type InspectInput struct {
	BaseURL   string
	AdapterID string
	Creds     domain.Credentials
	Opts      port.RouterOptions
}

// Execute gathers the report. It degrades gracefully: discovery works without
// credentials; authenticated sections are skipped (with a warning) when login is
// unavailable, rather than failing the whole command.
func (uc *InspectRouter) Execute(ctx context.Context, in InspectInput) (port.InspectReport, error) {
	var rep port.InspectReport

	info, err := uc.disco.Discover(ctx, in.BaseURL, in.Opts)
	if err != nil {
		return rep, err
	}
	rep.Info = info

	adapterID := in.AdapterID
	if adapterID == "" {
		adapterID = info.AdapterID
	}
	if adapterID == "" {
		rep.Warnings = append(rep.Warnings, "no adapter matched this router; protocol cannot be inspected (pass --adapter)")
		return rep, nil
	}
	if meta, ok := uc.factory.Lookup(adapterID); ok {
		rep.Meta = meta
		if !meta.Verified {
			rep.Warnings = append(rep.Warnings, "adapter endpoints are UNVERIFIED — confirm on a real router (see docs/reverse-engineering)")
		}
	}

	creds := in.Creds
	if !creds.HasPassword() {
		if loaded, ok, err := uc.vault.Load(ctx, RouterID(in.BaseURL)); err == nil && ok {
			creds = loaded
		}
	}
	gw, err := uc.factory.New(adapterID, in.BaseURL, in.Opts)
	if err != nil {
		return rep, err
	}
	if !creds.HasPassword() {
		rep.Warnings = append(rep.Warnings, "no credentials supplied or stored — authenticated sections skipped (run `kergui login`)")
		return rep, nil
	}
	if err := gw.Login(ctx, creds); err != nil {
		rep.Warnings = append(rep.Warnings, "login failed: "+err.Error())
		return rep, nil
	}
	rep.AuthOK = true

	// Enrich model/firmware from the authenticated status page.
	if ri, err := gw.RouterInfo(ctx); err == nil && ri != nil {
		if ri.Model != "" {
			rep.Info.Model = ri.Model
		}
		if ri.Firmware != "" {
			rep.Info.Firmware = ri.Firmware
		}
		if ri.HardwareVer != "" {
			rep.Info.HardwareVer = ri.HardwareVer
		}
		if rep.Info.Vendor == "" {
			rep.Info.Vendor = ri.Vendor
		}
	}

	if devs, err := gw.Devices(ctx); err == nil {
		rep.DeviceCount = len(devs)
	} else {
		rep.Warnings = append(rep.Warnings, "could not read devices: "+err.Error())
	}
	if rules, err := gw.AccessRules(ctx); err == nil {
		rep.AccessRuleCount = len(rules)
	} else {
		rep.Warnings = append(rep.Warnings, "could not read access rules: "+err.Error())
	}
	return rep, nil
}
