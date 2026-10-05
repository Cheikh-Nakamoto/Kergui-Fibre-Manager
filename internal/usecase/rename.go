package usecase

import (
	"context"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// RenameDevice sets the user's own label for a device (brief §6: a name
// independent of the router's hostname). This is a purely local change — it never
// touches the router.
type RenameDevice struct {
	devices port.DeviceRepository
}

// NewRenameDevice wires the interactor.
func NewRenameDevice(devices port.DeviceRepository) *RenameDevice {
	return &RenameDevice{devices: devices}
}

// RenameInput parameterises a rename.
type RenameInput struct {
	BaseURL string
	MAC     domain.MAC
	Name    string
}

// Execute updates the custom name. The device must already be in the inventory
// (run `devices` at least once); otherwise it returns domain.ErrDeviceNotFound.
func (uc *RenameDevice) Execute(ctx context.Context, in RenameInput) error {
	if in.MAC.IsZero() {
		return domain.ErrInvalidMAC
	}
	return uc.devices.SetCustomName(ctx, RouterID(in.BaseURL), in.MAC, in.Name)
}
