// Package zte implements a generic, profile-driven gateway for the ZTE ZXHN web
// UI family (F660, F680, Funbox, ...). A Profile captures everything that differs
// between models — endpoints, login field names, password hashing — so adding a
// model is data, not code. All endpoints here are DOCUMENTED HYPOTHESES: the
// Profile.Verified flag stays false until confirmed on real hardware.
package zte

import (
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// PasswordMode selects how the password form field is computed from the plaintext
// password and the per-session login token (brief: firmware variance).
type PasswordMode string

const (
	PasswordPlain       PasswordMode = "plain"        // field = password (older F660 firmware)
	PasswordSHA256      PasswordMode = "sha256"       // field = sha256(password)
	PasswordSHA256Token PasswordMode = "sha256_token" // field = sha256(password + token)
)

// LoginFields names the form fields the login POST must carry.
type LoginFields struct {
	Username    string            // e.g. "Username"
	Password    string            // e.g. "Password"
	Token       string            // hidden token field, e.g. "Frm_Logintoken" ("" if none)
	Action      string            // e.g. "action" ("" if none)
	ActionValue string            // e.g. "login"
	Extra       map[string]string // other fixed fields, e.g. {"_lang":"fr"}
}

// Profile describes one ZTE model's web interface.
type Profile struct {
	ID       string   // adapter id, e.g. "zte_f660"
	Vendor   string   // "ZTE"
	Models   []string // display names, e.g. ["F660","ZXHN F660"]
	Markers  []string // case-insensitive discovery fingerprints (title/body/Server)
	Verified bool     // confirmed on real hardware? (stays false until validated)

	// ReadReady is true when the read path (login + devices + ACL parsing) is
	// implemented for this model. Skeleton profiles leave it false and the
	// gateway returns domain.ErrNotImplemented for data operations.
	ReadReady bool

	SessionCookie string // cookie proving an authenticated session, e.g. "SID"
	PasswordMode  PasswordMode

	// Endpoints (relative URLs). Query strings are allowed; the mock/router match
	// on path + query.
	LoginPage   string // GET — page carrying the login token
	LoginSubmit string // POST — login submission
	StatusPage  string // GET — device info (model/firmware)
	DevicesPage string // GET — LAN hosts + WLAN associated devices
	ACLPage     string // GET — access control list

	// Write endpoints are DOCUMENTED ONLY in Milestone 1 (never called).
	BlockSubmit   string // POST (documented)
	UnblockSubmit string // POST (documented)

	Fields LoginFields
}

// Meta builds the adapter metadata advertised to the application, including the
// documented endpoint table used by `inspect`.
func (p Profile) Meta() domain.AdapterMeta {
	ep := func(op, method, path string) domain.EndpointDoc {
		return domain.EndpointDoc{Operation: op, Method: method, Path: path, Verified: p.Verified}
	}
	return domain.AdapterMeta{
		ID:       p.ID,
		Vendor:   p.Vendor,
		Models:   p.Models,
		Markers:  p.Markers,
		Verified: p.Verified,
		Endpoints: []domain.EndpointDoc{
			ep("login", "POST", p.LoginSubmit),
			ep("router_info", "GET", p.StatusPage),
			ep("devices", "GET", p.DevicesPage),
			ep("access_rules", "GET", p.ACLPage),
			{Operation: "block", Method: "POST", Path: p.BlockSubmit, Verified: false, Notes: "documented only; not implemented in M1"},
			{Operation: "unblock", Method: "POST", Path: p.UnblockSubmit, Verified: false, Notes: "documented only; not implemented in M1"},
		},
	}
}
