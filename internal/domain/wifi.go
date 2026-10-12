package domain

import (
	"fmt"
	"strings"
)

// WiFiSSID is one Wi-Fi network the router can broadcast. A dual-band router
// has several per band (SSID1-4 on 2.4 GHz, SSID5-8 on 5 GHz), most of them off.
type WiFiSSID struct {
	ID       string `json:"id"`       // router reference, e.g. DEV.WIFI.AP3
	Alias    string `json:"alias"`    // router label, e.g. SSID3
	Band     string `json:"band"`     // "2.4GHz", "5GHz", or "" if unknown
	Name     string `json:"name"`     // broadcast name (ESSID)
	Enabled  bool   `json:"enabled"`  // broadcast and accepting devices
	Hidden   bool   `json:"hidden"`   // name not broadcast
	Security string `json:"security"` // e.g. "WPA2/WPA3-SAE", "No Security"
	// Locked says why the router refuses changes to this SSID ("" when it
	// accepts them), e.g. a 5 GHz twin that follows its 2.4 GHz SSID.
	Locked string `json:"locked,omitempty"`
}

// WiFiSSIDChange is a partial update of an SSID: nil fields are left as is.
// The password is write-only: the router's is never read back to clients.
type WiFiSSIDChange struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	Name     *string `json:"name,omitempty"`
	Password *string `json:"password,omitempty"`
	Hidden   *bool   `json:"hidden,omitempty"`
}

// IsEmpty reports whether the change modifies nothing.
func (c WiFiSSIDChange) IsEmpty() bool {
	return c.Enabled == nil && c.Name == nil && c.Password == nil && c.Hidden == nil
}

// Validate checks the change against the router's own rules (printable ASCII,
// 1-32 characters for a name, 8-63 for a WPA password).
func (c *WiFiSSIDChange) Validate() error {
	if c.IsEmpty() {
		return fmt.Errorf("%w: aucune modification demandée", ErrInvalidInput)
	}
	if c.Name != nil {
		n := strings.TrimSpace(*c.Name)
		c.Name = &n
		if len(n) < 1 || len(n) > 32 || !printableASCII(n) {
			return fmt.Errorf("%w: nom du Wi-Fi requis (1 à 32 caractères ASCII)", ErrInvalidInput)
		}
	}
	if c.Password != nil {
		if p := *c.Password; len(p) < 8 || len(p) > 63 || !printableASCII(p) {
			return fmt.Errorf("%w: mot de passe Wi-Fi de 8 à 63 caractères ASCII", ErrInvalidInput)
		}
	}
	return nil
}

func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}
