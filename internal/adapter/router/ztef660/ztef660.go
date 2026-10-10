// Package ztef660 is the reference adapter: the ZTE ZXHN F660 "Fiberbox", the
// first environment target (Orange Sénégal). Its read path is implemented; its
// endpoints remain DOCUMENTED HYPOTHESES (Verified=false) until confirmed on a
// real device via the capture/validation loop.
package ztef660

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/zte"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// ID is the adapter id.
const ID = "zte_f660"

// Profile returns the F660 web-UI profile.
//
// NOTE: endpoints and field names are compiled from public ZTE documentation and
// the Orange Sénégal assistance pages (see RESEARCH.md). They are UNVERIFIED and
// deliberately centralised here so a single edit corrects them after a real
// capture. Password handling defaults to plaintext (older F660 firmware);
// newer firmware may require SHA256 — switch PasswordMode accordingly.
func Profile() zte.Profile {
	return zte.Profile{
		ID:            ID,
		Vendor:        "ZTE",
		Models:        []string{"F660", "ZXHN F660"},
		Markers:       []string{"f660", "zxhn f660"},
		Verified:      false,
		ReadReady:     true,
		SessionCookie: "SID",
		PasswordMode:  zte.PasswordPlain,

		LoginPage:   "/",
		LoginSubmit: "/",
		StatusPage:  "/getpage.gch?pid=1002&nextpage=status_device_info_t.gch",
		DevicesPage: "/getpage.gch?pid=1002&nextpage=net_lan_status_t.gch",
		ACLPage:     "/getpage.gch?pid=1002&nextpage=net_wlan_acl_t.gch",

		// Write path (UNVERIFIED): the ACL add/remove POST. WriteReady=true means
		// Block/Unblock actually fire this request (verified end-to-end against the
		// bundled mock); confirm the real fields on hardware before trusting it.
		WriteReady: true,
		WritePath:  "/setpage.gch",
		Write: zte.ACLWriteFields{
			Action:   "action",
			AddValue: "addMacFilter",
			DelValue: "delMacFilter",
			MAC:      "MACAddress",
			Mode:     "mode",
			ModeVal:  "black",
		},

		Fields: zte.LoginFields{
			Username:    "Username",
			Password:    "Password",
			Token:       "Frm_Logintoken",
			Action:      "action",
			ActionValue: "login",
			Extra:       map[string]string{"_lang": "fr"},
		},
	}
}

// Register adds the F660 adapter to a factory.
func Register(f *router.Factory) {
	p := Profile()
	f.Register(p.Meta(), func(baseURL string, opts port.RouterOptions) (port.RouterPort, error) {
		return zte.New(p, baseURL, opts)
	})
}
