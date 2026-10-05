package zte

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// The ZTE ZXHN web UI embeds tabular data as JavaScript arrays. These helpers
// extract that data. They are intentionally forgiving: unknown rows are skipped
// rather than failing the whole parse, because firmware revisions vary.

var (
	reToken = []*regexp.Regexp{
		regexp.MustCompile(`(?i)name="%s"[^>]*\bvalue="([^"]*)"`),
		regexp.MustCompile(`(?i)id="%s"[^>]*\bvalue="([^"]*)"`),
		regexp.MustCompile(`(?i)value="([^"]*)"[^>]*\bname="%s"`),
	}
)

// extractLoginToken finds the value of a hidden token input by field name.
// Returns "" when the field is absent (some firmwares use no token).
func extractLoginToken(html, field string) string {
	if field == "" {
		return ""
	}
	q := regexp.QuoteMeta(field)
	for _, tmpl := range reToken {
		re := regexp.MustCompile(strings.Replace(tmpl.String(), "%s", q, 1))
		if m := re.FindStringSubmatch(html); len(m) == 2 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// jsScalar returns the value of `var <name> = ...;`, with surrounding quotes and
// whitespace stripped.
func jsScalar(src, name string) string {
	re := regexp.MustCompile(`(?m)var\s+` + regexp.QuoteMeta(name) + `\s*=\s*(.+?);`)
	m := re.FindStringSubmatch(src)
	if len(m) != 2 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
}

// jsArrayRows returns the rows declared as `var <prefix>_0 = new Array(...)`,
// `var <prefix>_1 = ...` up to `var <prefix>_num = N`.
func jsArrayRows(src, prefix string) [][]string {
	n, _ := strconv.Atoi(jsScalar(src, prefix+"_num"))
	if n <= 0 || n > 4096 { // sanity bound
		return nil
	}
	rows := make([][]string, 0, n)
	for i := 0; i < n; i++ {
		re := regexp.MustCompile(`(?s)var\s+` + regexp.QuoteMeta(prefix) + `_` + strconv.Itoa(i) + `\s*=\s*new\s+Array\((.*?)\)\s*;`)
		m := re.FindStringSubmatch(src)
		if len(m) != 2 {
			continue
		}
		rows = append(rows, splitArgs(m[1]))
	}
	return rows
}

func splitArgs(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(strings.TrimSpace(p), `"'`))
	}
	return out
}

// parseDevices merges the LAN host table (hostname/ip/mac/active) with the WLAN
// associated-device table (mac/ssid) into a device inventory keyed by MAC.
func parseDevices(html string) []domain.Device {
	byMAC := map[string]*domain.Device{}
	order := []string{}

	for _, row := range jsArrayRows(html, "_lan_host") {
		if len(row) < 3 {
			continue
		}
		mac, err := domain.ParseMAC(row[2])
		if err != nil {
			continue
		}
		ip, _ := domain.ParseIP(row[1])
		d := &domain.Device{MAC: mac, IP: ip, Hostname: row[0]}
		if len(row) >= 4 && row[3] == "1" {
			d.Connected = true
		}
		byMAC[mac.String()] = d
		order = append(order, mac.String())
	}

	for _, row := range jsArrayRows(html, "_wlan_assoc") {
		if len(row) < 1 {
			continue
		}
		mac, err := domain.ParseMAC(row[0])
		if err != nil {
			continue
		}
		d, ok := byMAC[mac.String()]
		if !ok {
			d = &domain.Device{MAC: mac}
			byMAC[mac.String()] = d
			order = append(order, mac.String())
		}
		d.Connected = true // present on Wi-Fi now
		if len(row) >= 2 && d.SSID == "" {
			d.SSID = row[1]
		}
	}

	out := make([]domain.Device, 0, len(order))
	for _, k := range order {
		out = append(out, *byMAC[k])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC.String() < out[j].MAC.String() })
	return out
}

// parseAccessRules parses the ACL page into access rules. The per-row action, if
// present, wins; otherwise the list-wide mode applies.
func parseAccessRules(html string) []domain.AccessRule {
	listMode, _ := domain.ParseAccessMode(jsScalar(html, "_acl_mode"))
	rows := jsArrayRows(html, "_acl")
	out := make([]domain.AccessRule, 0, len(rows))
	for _, row := range rows {
		if len(row) < 1 {
			continue
		}
		mac, err := domain.ParseMAC(row[0])
		if err != nil {
			continue
		}
		mode := listMode
		if len(row) >= 2 {
			if m, err := domain.ParseAccessMode(row[1]); err == nil {
				mode = m
			}
		}
		if !mode.Valid() {
			mode = domain.AccessBlock // Black List is the ZTE default
		}
		out = append(out, domain.AccessRule{MAC: mac, Mode: mode, Source: "zte_acl"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MAC.String() < out[j].MAC.String() })
	return out
}

// parseRouterInfo reads model/firmware from the status page.
func parseRouterInfo(html string) (model, firmware, hardware string) {
	return jsScalar(html, "_model"), jsScalar(html, "_sw_ver"), jsScalar(html, "_hw_ver")
}
