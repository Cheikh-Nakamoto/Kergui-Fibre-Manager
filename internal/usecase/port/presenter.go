package port

import "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"

// Presenter is an output port: the use cases return plain data and the CLI
// controller hands it to a Presenter for rendering (table, JSON, ...). This keeps
// formatting out of the core and lets the output format be swapped freely.
type Presenter interface {
	Discovery(info domain.RouterInfo) error
	Devices(views []DeviceView) error
	Report(rep InspectReport) error
	Message(msg string) error
}
