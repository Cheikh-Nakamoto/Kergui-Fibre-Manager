// Package fixtures embeds SYNTHETIC router responses used by unit tests and by
// the bundled mock router. They are NOT captured from a real device: they model
// the documented ZTE ZXHN structure closely enough to exercise the parsers and
// the end-to-end flow, and they contain no real credentials, cookies or MAC
// addresses.
//
// To validate an adapter against reality, replace these with sanitised captures
// from your own router (see docs/reverse-engineering/README.md) and re-run the
// tests — the parsers, not these files, are the thing under test.
package fixtures

import _ "embed"

//go:embed zte_f660/login.html
var ZTEF660Login string

//go:embed zte_f660/devices.html
var ZTEF660Devices string

//go:embed zte_f660/access_control.html
var ZTEF660AccessControl string

//go:embed zte_f660/status.html
var ZTEF660Status string
