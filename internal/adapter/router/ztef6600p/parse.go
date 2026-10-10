package ztef6600p

import (
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// ajaxRoot is the top-level XML wrapper: <ajax_response_xml_root>.
type ajaxRoot struct {
	XMLName  xml.Name    `xml:"ajax_response_xml_root"`
	ErrorStr string      `xml:"IF_ERRORSTR"`
	Objects  []xmlObject `xml:",any"`
}

// xmlObject captures <OBJ_*_ID> elements.
type xmlObject struct {
	XMLName   xml.Name      `xml:""`
	Instances []xmlInstance  `xml:"Instance"`
	Encode    []string      `xml:"encode"`
}

// xmlInstance holds the flat <ParaName>/<ParaValue> pairs inside an Instance.
type xmlInstance struct {
	Entries []xmlEntry `xml:",any"`
}

type xmlEntry struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

// instanceMap converts the flat ParaName/ParaValue sequence into a map.
func (inst xmlInstance) toMap() map[string]string {
	m := make(map[string]string)
	var currentName string
	for _, e := range inst.Entries {
		switch e.XMLName.Local {
		case "ParaName":
			currentName = e.Value
		case "ParaValue":
			if currentName != "" {
				m[currentName] = e.Value
				currentName = ""
			}
		}
	}
	return m
}

func parseAjaxXML(data string) (ajaxRoot, error) {
	var root ajaxRoot
	if err := xml.Unmarshal([]byte(data), &root); err != nil {
		return root, err
	}
	return root, nil
}

func isSuccess(root ajaxRoot) bool {
	return root.ErrorStr == "" || root.ErrorStr == "SUCC"
}

// findObject returns the first object whose tag name contains substr.
func findObject(root ajaxRoot, substr string) *xmlObject {
	for i := range root.Objects {
		if strings.Contains(root.Objects[i].XMLName.Local, substr) {
			return &root.Objects[i]
		}
	}
	return nil
}

func parseRouterInfo(data string) (model, firmware, hardware string) {
	root, err := parseAjaxXML(data)
	if err != nil {
		return "", "", ""
	}
	obj := findObject(root, "DEVINFO")
	if obj == nil {
		return "", "", ""
	}
	for _, inst := range obj.Instances {
		m := inst.toMap()
		if v := m["ModelName"]; v != "" {
			model = v
		}
		if v := m["SoftwareVer"]; v != "" {
			firmware = v
		}
		if v := m["HardwareVer"]; v != "" {
			hardware = v
		}
	}
	return
}

func parseWLANClients(data string) []domain.Device {
	root, err := parseAjaxXML(data)
	if err != nil {
		return nil
	}

	// Build SSID lookup from OBJ_WLANAP_ID
	ssidMap := make(map[string]string) // _InstID (e.g. DEV.WIFI.AP1) -> ESSID
	if apObj := findObject(root, "WLANAP"); apObj != nil {
		for _, inst := range apObj.Instances {
			m := inst.toMap()
			ssidMap[m["_InstID"]] = m["ESSID"]
		}
	}

	// Parse device entries from OBJ_WLAN_AD_ID
	adObj := findObject(root, "WLAN_AD")
	if adObj == nil {
		return nil
	}
	var devs []domain.Device
	for _, inst := range adObj.Instances {
		m := inst.toMap()
		mac, err := domain.ParseMAC(m["MACAddress"])
		if err != nil {
			continue
		}
		ip, _ := domain.ParseIP(m["IPAddress"])
		d := domain.Device{
			MAC:       mac,
			IP:        ip,
			Hostname:  m["HostName"],
			Connected: true,
		}
		// Resolve SSID from AliasName (e.g. DEV.WIFI.AP1)
		if alias := m["AliasName"]; alias != "" {
			d.SSID = ssidMap[alias]
		}
		devs = append(devs, d)
	}
	return devs
}

// macFilterEntry is one MAC filter rule with the fields the router's form
// re-posts when the rule is deleted.
type macFilterEntry struct {
	InstID   string
	Name     string
	MAC      domain.MAC
	Type     string
	Protocol string
}

// firstOf returns the first non-empty value among keys. The web UI fills its
// form by matching ParaName to element ids (SrcMacAddr, _InstID, ...), but
// some firmwares prefix names with the object id, so both spellings are read.
func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
		if v := m["OBJ_MACFILTER_ID."+k]; v != "" {
			return v
		}
	}
	return ""
}

func parseMACFilterEntries(data string) []macFilterEntry {
	root, err := parseAjaxXML(data)
	if err != nil {
		return nil
	}
	obj := findObject(root, "MACFILTER")
	if obj == nil {
		return nil
	}
	var out []macFilterEntry
	for i, inst := range obj.Instances {
		m := inst.toMap()
		mac, err := domain.ParseMAC(firstOf(m, "SrcMacAddr", "SourceMACAddress", "MACAddress"))
		if err != nil {
			continue
		}
		e := macFilterEntry{
			InstID:   firstOf(m, "_InstID", "_OBJ_InstID"),
			Name:     firstOf(m, "Name"),
			MAC:      mac,
			Type:     firstOf(m, "Type"),
			Protocol: firstOf(m, "Protocol"),
		}
		if e.InstID == "" {
			e.InstID = fmt.Sprintf("%d", i+1)
		}
		out = append(out, e)
	}
	return out
}

func parseMACFilter(data string) []domain.AccessRule {
	var rules []domain.AccessRule
	for _, e := range parseMACFilterEntries(data) {
		rules = append(rules, domain.AccessRule{
			MAC:    e.MAC,
			Mode:   domain.AccessBlock,
			Source: ID,
			RawRef: e.InstID,
		})
	}
	return rules
}

// parseJSString extracts `name = "..."` from inline JS and decodes \xNN escapes
// (the firmware hex-escapes the session token in every menuView page).
func parseJSString(html, name string) string {
	idx := strings.Index(html, name+` = "`)
	if idx < 0 {
		return ""
	}
	rest := html[idx+len(name)+4:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	raw := rest[:end]
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+3 < len(raw) && raw[i+1] == 'x' {
			var c byte
			if _, err := fmt.Sscanf(raw[i+2:i+4], "%02x", &c); err == nil {
				b.WriteByte(c)
				i += 3
				continue
			}
		}
		b.WriteByte(raw[i])
	}
	return b.String()
}

func parseFilterGlobal(data string) (macEnabled bool, macTarget string) {
	root, err := parseAjaxXML(data)
	if err != nil {
		return false, ""
	}
	obj := findObject(root, "FWBASE")
	if obj == nil {
		return false, ""
	}
	for _, inst := range obj.Instances {
		m := inst.toMap()
		macEnabled = m["MacFilterEnable"] == "1"
		macTarget = m["MacFilterTarget"]
	}
	return
}

func parseLoginToken(data string) string {
	data = strings.TrimSpace(data)
	start := strings.Index(data, ">")
	end := strings.LastIndex(data, "<")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	return strings.TrimSpace(data[start+1 : end])
}
