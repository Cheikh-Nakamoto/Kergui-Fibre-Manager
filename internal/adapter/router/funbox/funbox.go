// Package funbox is a plug-in SKELETON for the Orange ZTE "Funbox". Discovery can
// fingerprint it; its data-page parsers await a real capture, so read operations
// return domain.ErrNotImplemented in Milestone 1.
package funbox

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/zte"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ID is the adapter id.
const ID = "zte_funbox"

// Profile returns the documented (but not-yet-read-ready) Funbox profile.
func Profile() zte.Profile {
	return zte.Profile{
		ID:            ID,
		Vendor:        "ZTE",
		Models:        []string{"Funbox", "ZTE Funbox"},
		Markers:       []string{"funbox"},
		Verified:      false,
		ReadReady:     false, // parsers pending a real capture
		SessionCookie: "SID",
		PasswordMode:  zte.PasswordPlain,

		LoginPage:   "/",
		LoginSubmit: "/",
		Fields: zte.LoginFields{
			Username:    "Username",
			Password:    "Password",
			Token:       "Frm_Logintoken",
			Action:      "action",
			ActionValue: "login",
		},
	}
}

// Register adds the Funbox skeleton to a factory.
func Register(f *router.Factory) {
	p := Profile()
	f.Register(p.Meta(), func(baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
		return zte.New(p, baseURL, opts)
	})
}
