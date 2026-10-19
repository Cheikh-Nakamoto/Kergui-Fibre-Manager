package mockrouter

// The stations present on the mock box, and the three pages that describe
// them: the Wi-Fi client table, the DHCP lease table and the topology.
//
// H1 served the three captured fixtures as they were, which was enough to read
// a device list. H2 needs a box where a device can arrive and leave, so the
// three pages are rendered from one station table instead. The table is seeded
// from the captured Wi-Fi client page, so the mock still starts out as the
// hardware did, and the three pages now agree on who is present — which is
// exactly what the hub's inventory diff reasons about.
//
// The captured DHCP and topology fixtures are still the input of the adapter's
// own parser tests (internal/adapter/router/ztef6600p); they are no longer the
// mock's source of truth.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// Station is one device the mock box can see. A test fills in what it cares
// about and Join completes the rest.
type Station struct {
	MAC      string
	Hostname string
	IP       string
	// AP is the access point it is associated with: DEV.WIFI.AP1..AP4 on
	// 2.4 GHz, AP5..AP8 on 5 GHz.
	AP string
	// LinkSecs is how long the current association has lasted. A counter that
	// moved backwards together with a shorter LinkSecs is how a consumer
	// spots a reconnection (domain.TrafficDelta).
	LinkSecs int
	Rx, Tx   uint64
	// Standard, BTM, RRM, Security and MaxLinkMbps are what the topology page
	// reports about the station's radio.
	Standard    string
	BTM, RRM    bool
	Security    string // OPEN | WEP | WPA | WPA2 | WPA3
	MaxLinkMbps int
	RSSI        int
	// LeaseSecs is the time left on its DHCP lease, and LeaseSeq the router's
	// lease instance number: a new DHCP transaction gets a new one.
	LeaseSecs int
	LeaseSeq  int
	// Associated false means it left the Wi-Fi but its lease has not expired,
	// so only the DHCP table still mentions it — the state a phone is in
	// right after it switches to mobile data.
	Associated bool
}

// bandCode maps an access point to the topology's AccessType: 0 LAN,
// 1 2.4 GHz, 2 5 GHz.
func (st Station) bandCode() string {
	n, err := strconv.Atoi(strings.TrimPrefix(st.AP, "DEV.WIFI.AP"))
	switch {
	case err != nil:
		return "0"
	case n <= 4:
		return "1"
	default:
		return "2"
	}
}

// dhcpPort is the physical port the DHCP table names: the SSID index of the
// access point, as the box writes it.
func (st Station) dhcpPort() string {
	n, err := strconv.Atoi(strings.TrimPrefix(st.AP, "DEV.WIFI.AP"))
	if err != nil {
		return "LAN1"
	}
	return fmt.Sprintf("SSID%d", n)
}

// beaconTypes are the codes the topology page uses for the security mode.
var beaconTypes = map[string]string{"OPEN": "0", "WEP": "1", "WPA": "2", "WPA2": "3", "WPA3": "10"}

// Join puts a station on the box, as a device joining the Wi-Fi. Zero fields
// take a plausible default, so a test writes only what it is about. Joining a
// MAC that is already there replaces it, which is how a test moves a device.
func (s *F6600P) Join(st Station) {
	if st.AP == "" {
		st.AP = "DEV.WIFI.AP1"
	}
	if st.LinkSecs == 0 {
		st.LinkSecs = 30
	}
	if st.Standard == "" {
		st.Standard = "11ax"
	}
	if st.Security == "" {
		st.Security = "WPA2"
	}
	if st.MaxLinkMbps == 0 {
		st.MaxLinkMbps = 300
	}
	if st.RSSI == 0 {
		st.RSSI = -60
	}
	if st.LeaseSecs == 0 {
		st.LeaseSecs = 86400
	}
	st.Associated = true

	s.mu.Lock()
	defer s.mu.Unlock()
	if st.LeaseSeq == 0 {
		s.nextLease++
		st.LeaseSeq = s.nextLease
	}
	for i := range s.stations {
		if strings.EqualFold(s.stations[i].MAC, st.MAC) {
			s.stations[i] = st
			return
		}
	}
	s.stations = append(s.stations, st)
}

// Leave takes a station off the Wi-Fi. Its DHCP lease survives, as on the box:
// a device that just left is still listed, unassociated.
func (s *F6600P) Leave(mac string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.stations {
		if strings.EqualFold(s.stations[i].MAC, mac) {
			s.stations[i].Associated = false
			s.stations[i].LinkSecs = 0
		}
	}
}

// Forget removes a station entirely, lease included.
func (s *F6600P) Forget(mac string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.stations[:0]
	for _, st := range s.stations {
		if !strings.EqualFold(st.MAC, mac) {
			kept = append(kept, st)
		}
	}
	s.stations = kept
}

// Stations returns a copy of the station table.
func (s *F6600P) Stations() []Station {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Station(nil), s.stations...)
}

// --- seeding from the captured page ---------------------------------------

var (
	reInstance = regexp.MustCompile(`(?s)<Instance>(.*?)</Instance>`)
	rePara     = regexp.MustCompile(`<ParaName>([^<]*)</ParaName><ParaValue>([^<]*)</ParaValue>`)
)

// parseParaInstances reads the box's ParaName/ParaValue instances out of one
// object of an ajax page. It is a small, deliberate restatement of the wire
// format: the adapter's parser is the code under test elsewhere, and a mock
// that borrowed it would hide a change to it.
func parseParaInstances(page, object string) []map[string]string {
	start := strings.Index(page, "<"+object+">")
	if start < 0 {
		return nil
	}
	end := strings.Index(page[start:], "</"+object+">")
	if end < 0 {
		return nil
	}
	var out []map[string]string
	for _, inst := range reInstance.FindAllStringSubmatch(page[start:start+end], -1) {
		m := map[string]string{}
		for _, p := range rePara.FindAllStringSubmatch(inst[1], -1) {
			m[p[1]] = unescapeXML(p[2])
		}
		out = append(out, m)
	}
	return out
}

func unescapeXML(s string) string {
	return strings.NewReplacer("&#32;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`).Replace(s)
}

// seedStations builds the station table from the captured Wi-Fi client page,
// so the mock starts in the state the hardware was in when it was captured.
func seedStations() []Station {
	var out []Station
	for i, m := range parseParaInstances(fixtures.ZTEF6600PWLANClients, "OBJ_WLAN_AD_ID") {
		if m["MACAddress"] == "" {
			continue
		}
		secs, _ := strconv.Atoi(strings.TrimSpace(m["LinkTime"]))
		rx, _ := strconv.ParseUint(strings.TrimSpace(m["RXBytes"]), 10, 64)
		tx, _ := strconv.ParseUint(strings.TrimSpace(m["TXBytes"]), 10, 64)
		rssi, _ := strconv.Atoi(strings.TrimSpace(m["RSSI"]))
		mbps, _ := strconv.Atoi(strings.TrimSpace(m["TxRate"]))
		out = append(out, Station{
			MAC: m["MACAddress"], Hostname: m["HostName"], IP: m["IPAddress"],
			AP: m["AliasName"], LinkSecs: secs, Rx: rx, Tx: tx,
			Standard: m["CurrentMode"], Security: "WPA2", MaxLinkMbps: mbps / 10,
			RSSI: rssi, LeaseSecs: 86400, LeaseSeq: i + 1, Associated: true,
		})
	}
	return out
}

// --- the three pages ------------------------------------------------------

// renderWLANClients is the Wi-Fi client table: the access points with their
// network names, then one row per associated station. A station that left is
// absent, exactly as on the box.
func renderWLANClients(ssids []ssidEntry, stations []Station) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORPARAM>SUCC</IF_ERRORPARAM><IF_ERRORTYPE>SUCC</IF_ERRORTYPE>` +
		`<IF_ERRORSTR>SUCC</IF_ERRORSTR><IF_ERRORID>0</IF_ERRORID><OBJ_WLAN_AD_ID>`)
	n := 0
	for _, st := range sortedStations(stations) {
		if !st.Associated {
			continue
		}
		n++
		b.WriteString(`<Instance>` +
			para("_InstID", fmt.Sprintf("%s.AD%d", st.AP, n)) +
			para("AliasName", st.AP) +
			para("RXBytes", strconv.FormatUint(st.Rx, 10)) +
			para("TXBytes", strconv.FormatUint(st.Tx, 10)) +
			para("HostName", st.Hostname) +
			para("LinkTime", strconv.Itoa(st.LinkSecs)) +
			para("IPAddress", st.IP) +
			para("MACAddress", st.MAC) +
			para("CurrentMode", st.Standard) +
			para("RSSI", strconv.Itoa(st.RSSI)) +
			para("TxRate", strconv.Itoa(st.MaxLinkMbps*10)) +
			`</Instance>`)
	}
	b.WriteString(`</OBJ_WLAN_AD_ID><OBJ_WLANAP_ID>`)
	for _, s := range ssids {
		b.WriteString(`<Instance>` + para("_InstID", s.id) + para("Alias", s.alias) +
			para("ESSID", s.name) + `</Instance>`)
	}
	b.WriteString(`</OBJ_WLANAP_ID></ajax_response_xml_root>`)
	return b.String()
}

// renderDHCPHosts is the lease table. It is the only page that remembers a
// station that left, which is what lets the inventory tell "gone" from "never
// seen".
func renderDHCPHosts(stations []Station) string {
	var b strings.Builder
	b.WriteString(`<ajax_response_xml_root><IF_ERRORSTR>SUCC</IF_ERRORSTR><OBJ_DHCPHOSTINFO_ID>`)
	for _, st := range sortedStations(stations) {
		b.WriteString(`<Instance>` +
			para("_InstID", fmt.Sprintf("DEV.V4DP.Sr.Pl1.Ct%d", st.LeaseSeq)) +
			para("MACAddr", st.MAC) +
			para("IPAddr", st.IP) +
			para("HostName", st.Hostname) +
			para("PhyPortName", st.dhcpPort()) +
			para("ExpiredTime", strconv.Itoa(st.LeaseSecs)) +
			`</Instance>`)
	}
	b.WriteString(`</OBJ_DHCPHOSTINFO_ID></ajax_response_xml_root>`)
	return b.String()
}

// renderTopology rebuilds the topology page's station map, keeping the rest of
// the captured JSON (the box itself and its mesh nodes) as it was.
func renderTopology(stations []Station) string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fixtures.ZTEF6600PTopology), &doc); err != nil {
		panic("mockrouter: captured topology is not JSON: " + err.Error())
	}
	ad := map[string]any{}
	i := 0
	for _, st := range sortedStations(stations) {
		if !st.Associated {
			continue // the topology only knows associated stations
		}
		host := st.Hostname
		if host == "" {
			host = st.MAC // the box repeats the MAC when it has no name
		}
		ad[strconv.Itoa(i)] = map[string]string{
			"MacAddr": st.MAC, "IpAddr": st.IP, "HostName": host,
			"AccessType": st.bandCode(), "WirelessMode": st.Standard,
			"Btm": boolCode(st.BTM), "Rrm": boolCode(st.RRM),
			"BeaconType": beaconTypes[st.Security],
			"LinkSpeed":  strconv.Itoa(st.MaxLinkMbps),
			"AssocTime":  strconv.Itoa(st.LinkSecs),
			"parent":     "MESH.CONTROLLER",
			"Rssi":       strconv.Itoa(st.RSSI),
		}
		i++
	}
	ad["MGET_INST_NUM"] = i
	raw, err := json.Marshal(ad)
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	doc["ad"] = raw
	out, err := json.Marshal(doc)
	if err != nil {
		panic("mockrouter: " + err.Error())
	}
	return string(out)
}

func boolCode(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// sortedStations keeps the rendered pages stable between two polls, so a diff
// of two reads shows what changed and nothing else.
func sortedStations(in []Station) []Station {
	out := append([]Station(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].MAC) < strings.ToLower(out[j].MAC)
	})
	return out
}
