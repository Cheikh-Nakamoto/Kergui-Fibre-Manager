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

// ACLWriteFields names the form fields of an access-control write (block/unblock)
// POST. Like the login fields, these are DOCUMENTED HYPOTHESES per model until a
// real capture confirms them; they are centralised here so one edit corrects the
// whole write path after validation.
type ACLWriteFields struct {
	Action   string // field carrying the operation, e.g. "action"
	AddValue string // value that adds a block rule, e.g. "addMacFilter"
	DelValue string // value that removes a block rule, e.g. "delMacFilter"
	MAC      string // field carrying the MAC address, e.g. "MACAddress"
	Mode     string // optional field naming the list, e.g. "mode" ("" if none)
	ModeVal  string // value for Mode, e.g. "black"
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

	// Write path for access-control changes. WriteReady gates execution: when
	// false, Block/Unblock return domain.ErrNotImplemented (skeleton models, or a
	// model whose write path has not been wired yet). The endpoints themselves
	// stay UNVERIFIED (see Verified) until confirmed on real hardware.
	WriteReady bool
	WritePath  string // POST endpoint for ACL changes, e.g. "/setpage.gch"
	Write      ACLWriteFields

	Fields LoginFields
}

// writeNote describes the state of the write path for `inspect`.
func writeNote(p Profile) string {
	if p.WriteReady {
		return "implemented; UNVERIFIED on hardware — confirm before trusting"
	}
	return "documented only; not implemented for this model yet"
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
			{Operation: "block", Method: "POST", Path: p.WritePath, Verified: p.Verified, Notes: writeNote(p)},
			{Operation: "unblock", Method: "POST", Path: p.WritePath, Verified: p.Verified, Notes: writeNote(p)},
		},
	}
}
