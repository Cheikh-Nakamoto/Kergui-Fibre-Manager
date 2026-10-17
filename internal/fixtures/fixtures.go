// Package fixtures embeds router response fixtures used by unit tests and by
// the bundled mock router. The zte_f660 fixtures are SYNTHETIC (modelled from
// documentation). The zte_f6600p fixtures are SANITISED captures from a real
// ZTE F6600P — real MAC addresses, hostnames and serial numbers have been
// replaced with placeholders (ac:bb:cc:00:00:xx, Phone-Salon, ZTEG00000001).
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

//go:embed zte_f6600p/status.xml
var ZTEF6600PStatus string

//go:embed zte_f6600p/wlan_clients.xml
var ZTEF6600PWLANClients string

//go:embed zte_f6600p/macfilter.xml
var ZTEF6600PMACFilter string

//go:embed zte_f6600p/filter_global.xml
var ZTEF6600PFilterGlobal string

//go:embed zte_f6600p/login_token.xml
var ZTEF6600PLoginToken string

// Recognition sources, captured in one poll on 2026-10-17 (firmware
// ZTEGF660006SN) and anonymised by a deterministic mapping that keeps what the
// parsers and the recognition depend on: the OUI of universal MACs, the
// locally-administered bit of random ones, model-like host names. Personal
// names, SSIDs and IPv6 interface identifiers are replaced.

//go:embed zte_f6600p/wlan_client_stat_live.xml
var ZTEF6600PWLANClientStatLive string

//go:embed zte_f6600p/dhcp_hostinfo.xml
var ZTEF6600PDHCPHostInfo string

//go:embed zte_f6600p/topo.json
var ZTEF6600PTopology string

//go:embed zte_f6600p/accessdev_data.xml
var ZTEF6600PAccessDevData string

//go:embed zte_f6600p/ra_raservice.xml
var ZTEF6600PRAService string

//go:embed zte_f6600p/session_timeout.xml
var ZTEF6600PSessionTimeout string
