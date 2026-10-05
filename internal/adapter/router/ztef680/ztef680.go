// Package ztef680 is a plug-in SKELETON for the ZTE ZXHN F680. Discovery can
// fingerprint it and its login profile is documented (3-step SHA256 + token login
// per the public ZTE-F680-API client), but its data-page parsers await a real
// capture, so read operations return domain.ErrNotImplemented in Milestone 1.
package ztef680

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/zte"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ID is the adapter id.
const ID = "zte_f680"

// Profile returns the documented (but not-yet-read-ready) F680 profile.
func Profile() zte.Profile {
	return zte.Profile{
		ID:            ID,
		Vendor:        "ZTE",
		Models:        []string{"F680", "ZXHN F680"},
		Markers:       []string{"f680", "zxhn f680"},
		Verified:      false,
		ReadReady:     false, // parsers pending a real capture
		SessionCookie: "SID",
		PasswordMode:  zte.PasswordSHA256Token,

		LoginPage:   "/",
		LoginSubmit: "/",
		// Data pages intentionally left blank until captured.
		Fields: zte.LoginFields{
			Username:    "Username",
			Password:    "Password",
			Token:       "Frm_Logintoken",
			Action:      "action",
			ActionValue: "login",
		},
	}
}

// Register adds the F680 skeleton to a factory.
func Register(f *router.Factory) {
	p := Profile()
	f.Register(p.Meta(), func(baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
		return zte.New(p, baseURL, opts)
	})
}
