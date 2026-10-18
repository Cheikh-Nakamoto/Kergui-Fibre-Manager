package domain

import (
	"testing"
	"time"
)

// base is a reference poll time for the scenarios.
var base = time.Date(2026, 10, 17, 20, 0, 0, 0, time.UTC)

// obsOpt mutates an observation under construction.
type obsOpt func(*Observation)

func at(t time.Time) obsOpt {
	return func(o *Observation) { o.At = t; o.AssocSince = t.Add(-time.Hour) }
}
func host(n string) obsOpt     { return func(o *Observation) { o.Hostname = n } }
func ipv4(s string) obsOpt     { return func(o *Observation) { o.IPv4 = MustIP(s) } }
func band(b string) obsOpt     { return func(o *Observation) { o.Band = b } }
func since(t time.Time) obsOpt { return func(o *Observation) { o.AssocSince = t } }
func offline() obsOpt {
	return func(o *Observation) { o.Associated = false; o.AssocSince = time.Time{} }
}

func radio(std string, btm, rrm bool) obsOpt {
	return func(o *Observation) { o.Radio = RadioCaps{Standard: std, BTM: btm, RRM: rrm} }
}

// lease sets a DHCPv4 lease granted `age` before the poll, lasting 24h.
func lease(age time.Duration) obsOpt {
	return func(o *Observation) {
		o.Lease = &Lease{GrantedAt: o.At.Add(-age), ExpiresAt: o.At.Add(24*time.Hour - age)}
	}
}

// ob builds one associated observation. By default it is a station seen at
// the current poll that associated a minute ago (a freshly-appeared MAC);
// at() backdates a historical observation.
func ob(mac string, opts ...obsOpt) Observation {
	o := Observation{MAC: MustMAC(mac), At: base, Associated: true, AssocSince: base.Add(-time.Minute), Band: "2.4G"}
	for _, f := range opts {
		f(&o)
	}
	return o
}

// dev wraps past observations as a known device.
func dev(id string, obs ...Observation) KnownDevice {
	return KnownDevice{ID: DeviceID(id), Observations: obs}
}

func recognise(t *testing.T, now Observation, known ...KnownDevice) Decision {
	t.Helper()
	return Recognise([]Observation{now}, known, nil, DefaultRecognitionRules)
}

// Scenario 1: same device, same MAC, new IP. The MAC is already bound, so the
// IP change is irrelevant: Associated.
func TestScenario1_SameMACNewIP(t *testing.T) {
	known := dev("DEVICE-000001", ob("60:bb:6f:00:00:09", at(base.Add(-2*time.Hour)), ipv4("192.168.1.3")))
	now := ob("60:bb:6f:00:00:09", ipv4("192.168.1.50"))
	d := recognise(t, now, known)
	if d.Outcome != Associated || d.Device != "DEVICE-000001" {
		t.Fatalf("got %s %s, want associated DEVICE-000001", d.Outcome, d.Device)
	}
}

// Scenario 2: new MAC, same IP, within the handoff window and same host name.
// The router handed the same IP to a new MAC right after the old one left:
// strong, but a new private MAC can be deliberate, so it is Proposed, not
// linked.
func TestScenario2_NewMACSameIP(t *testing.T) {
	known := dev("DEVICE-000002", ob("e6:15:cf:00:00:01", at(base.Add(-3*time.Minute)), host("S22"), ipv4("192.168.1.3")))
	now := ob("60:bb:6f:00:00:09", host("S22"), ipv4("192.168.1.3"))
	d := recognise(t, now, known)
	if d.Outcome != Proposed || len(d.Candidates) != 1 || d.Candidates[0].Device != "DEVICE-000002" {
		t.Fatalf("got %s %+v, want proposed DEVICE-000002", d.Outcome, d.Candidates)
	}
}

// Scenario 3: new MAC and new IP, host name kept, within the handoff window.
// This is the common "toggled private address" case: Proposed.
func TestScenario3_NewMACNewIP(t *testing.T) {
	known := dev("DEVICE-000003", ob("84:22:89:00:00:10", at(base.Add(-5*time.Minute)), host("A04e-de-Test"), ipv4("192.168.1.14")))
	now := ob("8a:26:78:00:00:03", host("A04e-de-Test"), ipv4("192.168.1.28"))
	d := recognise(t, now, known)
	if d.Outcome != Proposed {
		t.Fatalf("got %s, want proposed", d.Outcome)
	}
	if !d.Candidates[0].has(EvHostname) || !d.Candidates[0].has(EvHandoff) || !d.Candidates[0].has(EvMACChange) {
		t.Errorf("evidence = %+v, want hostname + handoff + mac-change", d.Candidates[0].Evidence)
	}
}

// Scenario 4: forget then rejoin the Wi-Fi (new MAC, short gap, same name):
// same as scenario 3 — Proposed.
func TestScenario4_ForgetRejoin(t *testing.T) {
	known := dev("DEVICE-000004", ob("aa:a4:55:00:00:01", at(base.Add(-10*time.Minute)), host("Galaxy-A51")))
	now := ob("a8:30:bc:00:00:0f", host("Galaxy-A51"))
	if d := recognise(t, now, known); d.Outcome != Proposed {
		t.Fatalf("got %s, want proposed", d.Outcome)
	}
}

// Scenario 5: phone reboot and DHCP renewal keep the MAC (Android keeps its
// per-network private MAC across reboots): Associated.
func TestScenario5_RebootRenew(t *testing.T) {
	known := dev("DEVICE-000005", ob("d6:00:00:00:00:05", at(base.Add(-time.Hour)), lease(50*time.Minute)))
	now := ob("d6:00:00:00:00:05", lease(1*time.Minute)) // fresh lease, same MAC
	if d := recognise(t, now, known); d.Outcome != Associated {
		t.Fatalf("got %s, want associated", d.Outcome)
	}
}

// Scenario 6: device gone for hours, back under a new MAC. The gap exceeds the
// handoff window, so the only link is the host name: Unassigned, kept as a
// hint the admin can act on.
func TestScenario6_LongAbsence(t *testing.T) {
	known := dev("DEVICE-000006", ob("82:e6:e8:00:00:01", at(base.Add(-14*time.Hour)), host("Redmi-Note-9S"), offline(), lease(0)))
	now := ob("4c:02:20:00:00:0d", host("Redmi-Note-9S"))
	d := recognise(t, now, known)
	if d.Outcome != Unassigned {
		t.Fatalf("got %s, want unassigned", d.Outcome)
	}
	if len(d.Candidates) != 1 || !d.Candidates[0].has(EvHostname) || d.Candidates[0].has(EvHandoff) {
		t.Errorf("want a single hostname hint without handoff, got %+v", d.Candidates)
	}
}

// Scenario 7: two phones of the same model with similar, distinct host names
// are never merged — the names differ, so there is no evidence at all.
func TestScenario7_SameModelDistinctNames(t *testing.T) {
	known := dev("DEVICE-000007", ob("28:ea:2d:00:00:0b", at(base.Add(-2*time.Minute)), host("iPhone-Awa")))
	now := ob("56:ce:28:00:00:01", host("iPhone-Bintou"))
	if d := recognise(t, now, known); d.Outcome != Unassigned || len(d.Candidates) != 0 {
		t.Fatalf("got %s %+v, want unassigned with no candidate", d.Outcome, d.Candidates)
	}
}

// Scenario 7b: two phones whose host name is literally "iPhone". The generic
// name is not an identity key, so it is never evidence.
func TestScenario7b_GenericName(t *testing.T) {
	known := dev("DEVICE-000017", ob("28:ea:2d:00:00:0b", at(base.Add(-2*time.Minute)), host("iPhone")))
	now := ob("56:ce:28:00:00:01", host("iPhone"))
	if d := recognise(t, now, known); d.Outcome != Unassigned || len(d.Candidates) != 0 {
		t.Fatalf("got %s %+v, want unassigned with no candidate", d.Outcome, d.Candidates)
	}
}

// Scenario 7c: the host name belongs to two devices online at once
// (sharedNames). Even with a handoff it must never be proposed.
func TestScenario7c_SharedName(t *testing.T) {
	known := dev("DEVICE-000027", ob("20:e8:74:00:00:10", at(base.Add(-3*time.Minute)), host("Make-money-TV"), offline()))
	now := ob("90:81:58:00:00:0c", host("Make-money-TV"))
	shared := map[string]bool{"make-money-tv": true}
	d := Recognise([]Observation{now}, []KnownDevice{known}, shared, DefaultRecognitionRules)
	if d.Outcome != Unassigned {
		t.Fatalf("got %s, want unassigned (shared name is not evidence)", d.Outcome)
	}
}

// Scenario 8: two devices with identical metadata but seen online together are
// kept apart by the co-presence veto. The known device's MAC is still
// associated at the same poll as the new MAC, overlapping by more than
// MinOverlap, so the matching host name is discarded and no candidate is kept.
func TestScenario8_CoPresentVeto(t *testing.T) {
	known := dev("DEVICE-000008", ob("9c:da:36:00:00:06", host("TECNO"), since(base.Add(-30*time.Minute))))
	now := ob("ca:6c:6d:00:00:01", host("TECNO"), since(base.Add(-20*time.Minute)))
	d := recognise(t, now, known)
	if d.Outcome != Unassigned || len(d.Candidates) != 0 {
		t.Fatalf("got %s %+v, want unassigned with no candidate (co-present devices must not merge)", d.Outcome, d.Candidates)
	}
}

// Scenario 8b: a brief overlap (make-before-break roaming) is below MinOverlap
// and does not veto. The two MACs are still online at the same poll, so there
// is no handoff; the matching name survives only as a hint, not a veto —
// contrast with scenario 8, where the candidate is dropped.
func TestScenario8b_BriefOverlapNoVeto(t *testing.T) {
	known := dev("DEVICE-000018", ob("9c:da:36:00:00:06", host("TECNO"), since(base.Add(-30*time.Minute))))
	now := ob("ca:6c:6d:00:00:01", host("TECNO"), since(base.Add(-30*time.Second)))
	d := recognise(t, now, known)
	if d.Outcome != Unassigned || len(d.Candidates) != 1 || !d.Candidates[0].has(EvHostname) {
		t.Fatalf("got %s %+v, want unassigned with a retained hostname hint", d.Outcome, d.Candidates)
	}
}

// Scenario 8c: a new MAC whose radio contradicts the candidate on the same
// band is a different device, whatever the name says.
func TestScenario8c_RadioMismatchVeto(t *testing.T) {
	known := dev("DEVICE-000028", ob("9c:da:36:00:00:06", at(base.Add(-5*time.Minute)), host("TECNO"),
		band("2.4G"), radio("802.11n", true, true), offline()))
	now := ob("ca:6c:6d:00:00:01", host("TECNO"), band("2.4G"), radio("802.11ax", false, false))
	if d := recognise(t, now, known); d.Outcome != Unassigned {
		t.Fatalf("got %s, want unassigned (radio contradicts on the same band)", d.Outcome)
	}
}

// Scenario 9: band / access-point change keeps the MAC: Associated, whatever
// the band and radio now report.
func TestScenario9_BandChange(t *testing.T) {
	known := dev("DEVICE-000009", ob("04:01:bb:00:00:08", at(base.Add(-time.Hour)), band("5G"), radio("802.11ac", true, true)))
	now := ob("04:01:bb:00:00:08", band("2.4G"), radio("802.11n", true, true))
	if d := recognise(t, now, known); d.Outcome != Associated {
		t.Fatalf("got %s, want associated", d.Outcome)
	}
}

// Scenario 10: temporary IPv6 addresses change constantly and are not used by
// recognition at all — only the MAC, DHCPv4 lease and radio are. A device seen
// again with the same MAC but a different (unused) IPv6 is still Associated.
func TestScenario10_TemporaryIPv6Ignored(t *testing.T) {
	known := dev("DEVICE-000010", ob("bc:a8:a6:00:00:0e", at(base.Add(-time.Hour))))
	now := ob("bc:a8:a6:00:00:0e")
	if d := recognise(t, now, known); d.Outcome != Associated {
		t.Fatalf("got %s, want associated (IPv6 plays no part)", d.Outcome)
	}
}

// A MAC with nothing in the registry starts a new provisional device.
func TestRecognise_Unknown(t *testing.T) {
	now := ob("38:92:31:00:00:11", host("TECNO-SPARK-40"))
	if d := recognise(t, now); d.Outcome != Unassigned || len(d.Candidates) != 0 {
		t.Fatalf("got %s %+v, want unassigned", d.Outcome, d.Candidates)
	}
}

// When two devices both match by name + handoff, both are proposed, most
// recently seen first; the admin chooses.
func TestRecognise_MultipleProposals(t *testing.T) {
	older := dev("DEVICE-000021", ob("11:22:33:00:00:01", at(base.Add(-12*time.Minute)), host("Shared")))
	newer := dev("DEVICE-000022", ob("11:22:33:00:00:02", at(base.Add(-4*time.Minute)), host("Shared")))
	now := ob("aa:bb:cc:00:00:03", host("Shared"))
	d := Recognise([]Observation{now}, []KnownDevice{older, newer}, nil, DefaultRecognitionRules)
	if d.Outcome != Proposed || len(d.Candidates) != 2 {
		t.Fatalf("got %s with %d candidates, want proposed with 2", d.Outcome, len(d.Candidates))
	}
	if d.Candidates[0].Device != "DEVICE-000022" {
		t.Errorf("first proposal = %s, want the most recently seen DEVICE-000022", d.Candidates[0].Device)
	}
}
