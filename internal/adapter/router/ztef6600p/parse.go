package ztef6600p

import (
	"encoding/xml"
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

func parseMACFilter(data string) []domain.AccessRule {
	root, err := parseAjaxXML(data)
	if err != nil {
		return nil
	}
	obj := findObject(root, "MACFILTER")
	if obj == nil {
		return nil
	}
	var rules []domain.AccessRule
	for _, inst := range obj.Instances {
		m := inst.toMap()
		macStr := m["OBJ_MACFILTER_ID.SourceMACAddress"]
		if macStr == "" {
			macStr = m["SourceMACAddress"]
		}
		mac, err := domain.ParseMAC(macStr)
		if err != nil {
			continue
		}
		rules = append(rules, domain.AccessRule{
			MAC:  mac,
			Mode: domain.AccessBlock,
		})
	}
	return rules
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
