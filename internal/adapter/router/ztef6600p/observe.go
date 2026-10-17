package ztef6600p

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Recognition sources, observed live 2026-10-17 on firmware ZTEGF660006SN with
// the "user" account. Each data tag answers only right after its own menuView.
const (
	dhcpHostsView = "lanMgrIpv4"
	dhcpHostsTag  = "Localnet_LanMgrIpv4_DHCPHostInfo_lua.lua"
	topoView      = "mmTopology"
	topoTag       = "topo_lua.lua" // JSON, not XML

	// assumedLeaseTime: the "user" account cannot read the DHCP lease time
	// (its form is in the lanMgrIpv4 view but the data object is withheld).
	// ExpiredTime never exceeded 86400 s in the captures.
	assumedLeaseTime = 24 * time.Hour
)

var _ port.HostObserver = (*Gateway)(nil)

// Observe implements port.HostObserver: one poll of the Wi-Fi association
// table, the DHCPv4 lease table and the topology, merged per MAC. Leases stay
// listed for their whole lifetime, so a MAC that left within the last lease
// time is still reported (Associated false).
func (g *Gateway) Observe(ctx context.Context) ([]domain.Observation, error) {
	at := time.Now()
	wlan, err := g.menuData(ctx, "localNetStatus", "wlan_client_stat_lua.lua")
	if err != nil {
		return nil, err
	}
	leases, err := g.menuData(ctx, dhcpHostsView, dhcpHostsTag)
	if err != nil {
		return nil, err
	}
	topo, err := g.menuData(ctx, topoView, topoTag)
	if err != nil {
		return nil, err
	}
	stations, err := parseTopology(string(topo))
	if err != nil {
		g.log.Warnf("f6600p: topology unreadable, radio capabilities skipped: %v", err)
	}
	return mergeObservations(at, parseWLANStations(string(wlan)), parseSSIDNames(string(wlan)),
		parseDHCPHosts(string(leases)), stations), nil
}

// parseSSIDNames maps the port names of the DHCP table ("SSID1") to network
// names, from the OBJ_WLANAP_ID part of the Wi-Fi client table.
func parseSSIDNames(data string) map[string]string {
	insts, _ := parseInstances(data, "WLANAP")
	out := map[string]string{}
	for _, m := range insts {
		out[m["Alias"]] = m["ESSID"]
	}
	return out
}

// wlanStation is one row of OBJ_WLAN_AD_ID beyond what parseWLANClients keeps.
type wlanStation struct {
	MAC      domain.MAC
	IP       domain.IP
	HostName string
	AP       string // "DEV.WIFI.AP1"
	SSID      string
	LinkSecs  int // seconds since association; ConnectTime is 1970 before SNTP sync
}

func parseWLANStations(data string) []wlanStation {
	root, err := parseAjaxXML(data)
	if err != nil {
		return nil
	}
	ssid := map[string]string{}
	if ap := findObject(root, "WLANAP"); ap != nil {
		for _, inst := range ap.Instances {
			m := inst.toMap()
			ssid[m["_InstID"]] = m["ESSID"]
		}
	}
	ad := findObject(root, "WLAN_AD")
	if ad == nil {
		return nil
	}
	var out []wlanStation
	for _, inst := range ad.Instances {
		m := inst.toMap()
		mac, err := domain.ParseMAC(m["MACAddress"])
		if err != nil {
			continue
		}
		ip, _ := domain.ParseIP(m["IPAddress"])
		secs, _ := strconv.Atoi(strings.TrimSpace(m["LinkTime"]))
		out = append(out, wlanStation{
			MAC: mac, IP: ip, HostName: m["HostName"],
			AP: m["AliasName"], SSID: ssid[m["AliasName"]], LinkSecs: secs,
		})
	}
	return out
}

// dhcpHost is one row of OBJ_DHCPHOSTINFO_ID.
type dhcpHost struct {
	MAC       domain.MAC
	IP        domain.IP
	HostName  string
	Port      string // "SSID1" .. "SSID8", "LAN1" ..
	Seq       int    // N of "DEV.V4DP.Sr.Pl1.CtN"
	Remaining time.Duration
}

func parseDHCPHosts(data string) []dhcpHost {
	insts, ok := parseInstances(data, "DHCPHOSTINFO")
	if !ok {
		return nil
	}
	var out []dhcpHost
	for _, m := range insts {
		mac, err := domain.ParseMAC(m["MACAddr"])
		if err != nil {
			continue
		}
		ip, _ := domain.ParseIP(m["IPAddr"])
		id := m["_InstID"]
		seq, _ := strconv.Atoi(id[strings.LastIndex(id, ".Ct")+len(".Ct"):])
		secs, _ := strconv.Atoi(strings.TrimSpace(m["ExpiredTime"]))
		out = append(out, dhcpHost{
			MAC: mac, IP: ip, HostName: m["HostName"], Port: m["PhyPortName"],
			Seq: seq, Remaining: time.Duration(secs) * time.Second,
		})
	}
	return out
}

// topoStation is one station of the topology page ("ad" map).
type topoStation struct {
	MAC      domain.MAC
	IP       domain.IP
	HostName string // empty when the router only knows the MAC
	Band     string
	Radio    domain.RadioCaps
	Assoc    time.Duration
	Parent   string // mesh node, "MESH.CONTROLLER" for the box itself
	RSSI     int
}

type topoJSON struct {
	Master map[string]any             `json:"master"`
	Slave  []json.RawMessage          `json:"slave"`
	AD     map[string]json.RawMessage `json:"ad"`
}

// parseTopology reads topo_lua.lua. The web UI maps AccessType 0/1/2 to
// LAN/2.4G/5G and BeaconType 0/1/2/3/10 to OPEN/WEP/WPA/WPA2/WPA3.
func parseTopology(data string) ([]topoStation, error) {
	var t topoJSON
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return nil, fmt.Errorf("%w: topology is not JSON: %v", domain.ErrUnexpectedResponse, err)
	}
	keys := make([]string, 0, len(t.AD))
	for k := range t.AD {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	var out []topoStation
	for _, k := range keys {
		var s map[string]any
		if json.Unmarshal(t.AD[k], &s) != nil {
			continue // "MGET_INST_NUM": 14
		}
		str := func(f string) string { v, _ := s[f].(string); return v }
		num := func(f string) int { n, _ := strconv.Atoi(str(f)); return n }
		mac, err := domain.ParseMAC(str("MacAddr"))
		if err != nil {
			continue
		}
		ip, _ := domain.ParseIP(str("IpAddr"))
		host := str("HostName")
		if hm, err := domain.ParseMAC(host); err == nil && hm.Equal(mac) {
			host = ""
		}
		out = append(out, topoStation{
			MAC: mac, IP: ip, HostName: host,
			Band: map[string]string{"0": "LAN", "1": "2.4G", "2": "5G"}[str("AccessType")],
			Radio: domain.RadioCaps{
				Standard:    str("WirelessMode"),
				BTM:         str("Btm") == "1",
				RRM:         str("Rrm") == "1",
				Security:    map[string]string{"0": "OPEN", "1": "WEP", "2": "WPA", "3": "WPA2", "10": "WPA3"}[str("BeaconType")],
				MaxLinkMbps: num("LinkSpeed"),
			},
			Assoc:  time.Duration(num("AssocTime")) * time.Second,
			Parent: str("parent"),
			RSSI:   num("Rssi"),
		})
	}
	return out, nil
}

// apBand maps the F6600P access points to radios: AP1-4 are on the 2.4 GHz
// radio, AP5-8 on the 5 GHz one (wlan_wlanstatus channels 6 and 112).
func apBand(ap string) string {
	n, err := strconv.Atoi(strings.TrimPrefix(ap, "DEV.WIFI.AP"))
	switch {
	case err != nil:
		return ""
	case n <= 4:
		return "2.4G"
	default:
		return "5G"
	}
}

// mergeObservations joins the three tables on the MAC. DHCP is the only table
// that remembers stations that left; Wi-Fi and topology list associated ones.
func mergeObservations(at time.Time, wlan []wlanStation, ssidByPort map[string]string, leases []dhcpHost, topo []topoStation) []domain.Observation {
	byMAC := map[string]*domain.Observation{}
	get := func(m domain.MAC) *domain.Observation {
		o, ok := byMAC[m.String()]
		if !ok {
			o = &domain.Observation{MAC: m, At: at}
			byMAC[m.String()] = o
		}
		return o
	}
	for _, w := range wlan {
		o := get(w.MAC)
		o.Associated = true
		o.IPv4, o.Hostname, o.SSID, o.Band = w.IP, w.HostName, w.SSID, apBand(w.AP)
		o.AssocSince = at.Add(-time.Duration(w.LinkSecs) * time.Second)
	}
	for _, l := range leases {
		o := get(l.MAC)
		o.Lease = &domain.Lease{
			Seq: l.Seq, Port: l.Port,
			ExpiresAt: at.Add(l.Remaining),
			GrantedAt: at.Add(l.Remaining - assumedLeaseTime),
		}
		if o.IPv4.IsZero() {
			o.IPv4 = l.IP
		}
		if l.HostName != "" {
			o.Hostname = l.HostName // the DHCP table fills names the Wi-Fi table misses
		}
		if o.SSID == "" {
			o.SSID = ssidByPort[l.Port]
		}
	}
	for _, s := range topo {
		o := get(s.MAC)
		o.Associated = true
		o.Radio = s.Radio
		if s.Band != "" {
			o.Band = s.Band
		}
		if o.Hostname == "" {
			o.Hostname = s.HostName
		}
		if o.IPv4.IsZero() {
			o.IPv4 = s.IP
		}
		if o.AssocSince.IsZero() {
			o.AssocSince = at.Add(-s.Assoc)
		}
	}
	out := make([]domain.Observation, 0, len(byMAC))
	for _, o := range byMAC {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC.String() < out[j].MAC.String() })
	return out
}
