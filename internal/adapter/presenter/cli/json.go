package cli

import (
	"encoding/json"
	"io"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// JSONPresenter renders results as indented JSON for scripting.
type JSONPresenter struct{ w io.Writer }

var _ port.Presenter = (*JSONPresenter)(nil)

// NewJSONPresenter writes to w.
func NewJSONPresenter(w io.Writer) *JSONPresenter { return &JSONPresenter{w: w} }

func (p *JSONPresenter) encode(v any) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Discovery renders the fingerprint result.
func (p *JSONPresenter) Discovery(info domain.RouterInfo) error { return p.encode(info) }

// Devices renders the inventory list.
func (p *JSONPresenter) Devices(views []port.DeviceView) error {
	if views == nil {
		views = []port.DeviceView{}
	}
	return p.encode(views)
}

// Report renders the inspect diagnostic.
func (p *JSONPresenter) Report(rep port.InspectReport) error { return p.encode(rep) }

// Message renders a message object.
func (p *JSONPresenter) Message(msg string) error {
	return p.encode(map[string]string{"message": msg})
}
