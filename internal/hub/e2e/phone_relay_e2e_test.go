package e2e

// The phone relay (issue #45): the relay loop runs inside a phone's Go core
// (mobilecore.RelayStart), on internal/agent like the PC relay, against the
// real hub and the mock F6600P. The Android foreground service only starts
// and stops it and answers the RelayNetwork questions, which the fake
// network below stands in for.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/mobilecore"
)

// phoneWiFi is mobilecore.RelayNetwork as RelayService implements it.
type phoneWiFi struct {
	ssid, bssid string
	binds       atomic.Int32
}

func (n *phoneWiFi) SSID() string  { return n.ssid }
func (n *phoneWiFi) BSSID() string { return n.bssid }
func (n *phoneWiFi) LANIP() string { return "192.168.1.60" }
func (n *phoneWiFi) BindSocket(int) bool {
	n.binds.Add(1)
	return true
}

type phoneRelayStatus struct {
	Running    bool   `json:"running"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	Check      string `json:"check"`
	AutoReject bool   `json:"autoReject"`
}

func relayStatus(t *testing.T, phone *mobilecore.Core) phoneRelayStatus {
	t.Helper()
	var st phoneRelayStatus
	if err := json.Unmarshal([]byte(phone.RelayStatus()), &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func waitForRelayState(t *testing.T, phone *mobilecore.Core, want string) phoneRelayStatus {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st := relayStatus(t, phone); st.State == want {
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("phone relay = %+v, want %s", relayStatus(t, phone), want)
	return phoneRelayStatus{}
}

// pairPhoneRelay is the onboarding of a phone left at home: the relay
// phone redeems the owner's QR, shows the same six digits, is signed into the bundle — whose
// network is the box's pin and the Wi-Fi "MonWiFi-2G" — and gets the box
// password sealed for it. The owner signs with the bench's keys, so the
// tests can sign commands too.
func pairPhoneRelay(t *testing.T, w *world) *mobilecore.Core {
	t.Helper()
	w.owner.registerNetwork(w.boxPin)
	w.owner.publishBundle(w.boxPin) // the owner alone, so the relay can pin the key

	phone, err := mobilecore.Open(t.TempDir(), "relay-phone-vault", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = phone.Close() })
	raw, err := phone.RelayEnroll(w.hub.URL, w.owner.invite("dedicated").String(), "Téléphone du salon")
	if err != nil {
		t.Fatalf("RelayEnroll: %v", err)
	}
	var enrolled struct{ AgentID, Code string }
	_ = json.Unmarshal([]byte(raw), &enrolled)
	listed := (&phoneOwnerApp{ownerApp: w.owner}).agent(enrolled.AgentID)
	ed, _ := base64.StdEncoding.DecodeString(listed.Ed25519)
	x, _ := base64.StdEncoding.DecodeString(listed.X25519)
	keys := trust.PublicKeys{Ed25519: ed, X25519: x}
	if trust.PairingCode(w.owner.keys.Public(), keys) != enrolled.Code {
		t.Fatal("the two pairing codes differ")
	}
	w.owner.publishBundle(w.boxPin, trust.Relay{AgentID: enrolled.AgentID, Kind: "dedicated", Keys: keys})
	w.owner.sealCredentials(enrolled.AgentID, x)

	// "Oui, c'est le même code" reached the phone; it never went through
	// the connection screen, so it finds the box by the pin the owner
	// signed, among the addresses the service suggests.
	out, err := phone.RelayCheckTrust()
	if err != nil || !strings.Contains(out, `"trusted":true`) {
		t.Fatalf("RelayCheckTrust = %s, %v", out, err)
	}
	if got, err := phone.RelayFindBox("https://127.0.0.1:1," + w.boxURL); err != nil || got != w.boxURL {
		t.Fatalf("RelayFindBox = %q, %v", got, err)
	}
	return phone
}

// TestE2E_PhoneRelayRunsFromTheCore: started by the service, the phone's
// core takes the lease, logs in with the password the owner sealed, pushes
// the device list, runs a block the owner signed, and stands down cleanly.
func TestE2E_PhoneRelayRunsFromTheCore(t *testing.T) {
	w := newWorldWith(t, failoverTimings)
	phone := pairPhoneRelay(t, w)

	wifi := &phoneWiFi{ssid: "MonWiFi-2G", bssid: "aa:bb:cc:dd:ee:01"}
	if err := phone.RelayStart(wifi); err != nil {
		t.Fatalf("RelayStart: %v", err)
	}
	st := waitForRelayState(t, phone, agent.StatusActive)
	if st.Check != agent.CheckSignature {
		t.Fatalf("status = %+v, want the Wi-Fi signature checked", st)
	}
	if devices := waitForDevices(t, w); !strings.Contains(devices, targetMAC) {
		t.Fatalf("the phone relay's snapshot lacks %s: %s", targetMAC, truncate(devices))
	}
	if wifi.binds.Load() == 0 {
		t.Fatal("no socket was bound to the home Wi-Fi")
	}

	if code, body := w.owner.block(targetMAC); code != http.StatusOK {
		t.Fatalf("POST block = %d %v", code, body)
	}
	if !w.box.Blocked(targetMAC) {
		t.Fatal("the mock box did not apply the block the phone relay ran")
	}

	phone.RelayStop()
	if st := relayStatus(t, phone); st.Running {
		t.Fatalf("still running after RelayStop: %+v", st)
	}
	// Stood down cleanly: the hub has no holder any more.
	waitForLease(t, w, func(l lease) bool { return l.holder == "" })
}

// TestE2E_PhoneRelayOnTheWrongWiFiNeverLogsIn: the box answers, the sealed
// password is there, but the phone is on a neighbour's Wi-Fi.
func TestE2E_PhoneRelayOnTheWrongWiFiNeverLogsIn(t *testing.T) {
	w := newWorldWith(t, failoverTimings)
	phone := pairPhoneRelay(t, w)
	before := w.box.Logins()

	if err := phone.RelayStart(&phoneWiFi{ssid: "Voisin"}); err != nil {
		t.Fatal(err)
	}
	st := waitForRelayState(t, phone, agent.StatusWrongNetwork)
	if !strings.Contains(st.Reason, "Wi-Fi") {
		t.Fatalf("status = %+v, want the reason", st)
	}
	time.Sleep(2 * time.Second) // several of the hub's heartbeats
	if n := w.box.Logins(); n != before {
		t.Fatalf("a relay on the wrong Wi-Fi logged in to the box %d times", n-before)
	}
	if code, _ := w.owner.call("GET", w.owner.tenantPath("router/devices"), nil); code != http.StatusNotFound {
		t.Errorf("GET devices = %d, want 404: a relay on the wrong Wi-Fi pushes nothing", code)
	}
}

// TestE2E_PhoneRelayWithoutLocationChecksTheBoxOnly: the location
// permission was refused, so Android gives no SSID: the box identity alone
// lets it relay, and its status says so.
func TestE2E_PhoneRelayWithoutLocationChecksTheBoxOnly(t *testing.T) {
	w := newWorldWith(t, failoverTimings)
	phone := pairPhoneRelay(t, w)

	if err := phone.RelayStart(&phoneWiFi{}); err != nil {
		t.Fatal(err)
	}
	st := waitForRelayState(t, phone, agent.StatusActive)
	if st.Check != agent.CheckBoxIdentity || !strings.Contains(st.Reason, "location") {
		t.Fatalf("status = %+v, want the box identity alone, said", st)
	}
	waitForDevices(t, w)
}
