package ztef6600p

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

func TestParseDHCPHosts(t *testing.T) {
	hosts := parseDHCPHosts(fixtures.ZTEF6600PDHCPHostInfo)
	if len(hosts) != 21 {
		t.Fatalf("got %d leases, want 21", len(hosts))
	}
	h := hosts[0]
	if h.MAC.String() != "a8:30:bc:00:00:0f" || h.IP.String() != "192.168.1.5" || h.HostName != "Galaxy-A51" ||
		h.Port != "SSID1" || h.Seq != 4 || h.Remaining != 79846*time.Second {
		t.Errorf("first lease = %+v", h)
	}
	// The router numbers every grant: sequence numbers are unique and follow
	// the grant order, so they grow with the remaining lease time.
	seen := map[int]bool{}
	for i, h := range hosts {
		if seen[h.Seq] {
			t.Errorf("duplicate lease sequence %d", h.Seq)
		}
		seen[h.Seq] = true
		if i > 0 && (h.Seq <= hosts[i-1].Seq || h.Remaining < hosts[i-1].Remaining) {
			t.Errorf("lease %d (Ct%d, %s) out of grant order", i, h.Seq, h.Remaining)
		}
	}
}

func TestParseDHCPHosts_SessionTimeout(t *testing.T) {
	if got := parseDHCPHosts(fixtures.ZTEF6600PSessionTimeout); got != nil {
		t.Errorf("SessionTimeout document parsed as %d leases", len(got))
	}
}

func TestParseTopology(t *testing.T) {
	st, err := parseTopology(fixtures.ZTEF6600PTopology)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 14 {
		t.Fatalf("got %d stations, want 14 (MGET_INST_NUM must be skipped)", len(st))
	}
	by := map[string]topoStation{}
	for _, s := range st {
		by[s.MAC.String()] = s
	}
	oppo := by["d6:00:00:00:00:05"]
	want := domain.RadioCaps{Standard: "802.11ax", BTM: true, RRM: true, Security: "WPA3", MaxLinkMbps: 2401}
	if oppo.Radio != want || oppo.Band != "5G" || oppo.Parent != "MESH.CONTROLLER" || oppo.Assoc != 6518*time.Second {
		t.Errorf("OPPO station = %+v", oppo)
	}
	if tv := by["38:e7:c0:00:00:0a"]; tv.Radio.BTM || tv.Radio.RRM || tv.Band != "2.4G" {
		t.Errorf("TV without 802.11k/v = %+v", tv)
	}
	if redmi := by["4c:02:20:00:00:0d"]; redmi.Radio.Security != "WPA2" {
		t.Errorf("Redmi negotiated %q, want WPA2 (BeaconType 3)", redmi.Radio.Security)
	}
	// The topology repeats the MAC as host name when it knows none.
	if n := by["d6:00:00:00:00:07"].HostName; n != "" {
		t.Errorf("host name = %q, want empty", n)
	}
}

func TestParseTopology_NotJSON(t *testing.T) {
	if _, err := parseTopology(fixtures.ZTEF6600PSessionTimeout); !errors.Is(err, domain.ErrUnexpectedResponse) {
		t.Errorf("err = %v, want ErrUnexpectedResponse", err)
	}
}

func liveObservations(t *testing.T, at time.Time) map[string]domain.Observation {
	t.Helper()
	topo, err := parseTopology(fixtures.ZTEF6600PTopology)
	if err != nil {
		t.Fatal(err)
	}
	obs := mergeObservations(at, parseWLANStations(fixtures.ZTEF6600PWLANClientStatLive),
		parseSSIDNames(fixtures.ZTEF6600PWLANClientStatLive), parseDHCPHosts(fixtures.ZTEF6600PDHCPHostInfo), topo)
	out := map[string]domain.Observation{}
	for _, o := range obs {
		out[o.MAC.String()] = o
	}
	return out
}

func TestMergeObservations(t *testing.T) {
	at := time.Date(2026, 10, 17, 20, 51, 0, 0, time.UTC)
	obs := liveObservations(t, at)
	if len(obs) != 21 {
		t.Fatalf("got %d stations, want 21 (14 associated + 7 known only by their lease)", len(obs))
	}
	assoc := 0
	for _, o := range obs {
		if o.Associated {
			assoc++
		}
		if o.Lease == nil {
			t.Errorf("%s has no lease: every station of this capture has one", o.MAC)
		}
	}
	if assoc != 14 {
		t.Errorf("%d associated, want 14", assoc)
	}

	// Left the Wi-Fi, still listed by its lease.
	gone := obs["a8:30:bc:00:00:0f"]
	if gone.Associated || gone.Hostname != "Galaxy-A51" || gone.SSID != "KerguiNet" || gone.Radio.Known() {
		t.Errorf("lease-only station = %+v", gone)
	}

	oppo := obs["d6:00:00:00:00:05"]
	if !oppo.Associated || oppo.SSID != "KerguiPerso" || oppo.Band != "5G" || oppo.Radio.Standard != "802.11ax" {
		t.Errorf("OPPO = %+v", oppo)
	}
	if !oppo.AssocSince.Equal(at.Add(-6521 * time.Second)) {
		t.Errorf("AssocSince = %s, want at-LinkTime", oppo.AssocSince)
	}
	if !oppo.Lease.GrantedAt.Equal(at.Add(79879*time.Second - 24*time.Hour)) {
		t.Errorf("GrantedAt = %s", oppo.Lease.GrantedAt)
	}
	// The Wi-Fi table had no name for this station; the DHCP table had one.
	if v := obs["d6:00:00:00:00:07"]; v.Hostname != "VILLAON-V40" {
		t.Errorf("VILLAON = %+v", v)
	}
	// Band steering: leased on the 5 GHz SSID5, now associated on 2.4 GHz,
	// same MAC (same SSID name and security on both bands).
	if tv := obs["04:01:bb:00:00:08"]; tv.Band != "2.4G" || tv.Lease.Port != "SSID5" {
		t.Errorf("band change not visible: band %s, lease port %s", tv.Band, tv.Lease.Port)
	}
}

func TestGateway_Observe(t *testing.T) {
	state := &mockState{viewLoaded: map[string]bool{}, live: true}
	srv := testServerWithState("user", "secret", state)
	defer srv.Close()
	gw := loginGateway(t, srv.URL)

	obs, err := gw.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(obs) != 21 {
		t.Errorf("got %d observations, want 21", len(obs))
	}
	for _, o := range obs {
		if o.At.IsZero() {
			t.Fatalf("%s has no poll time", o.MAC)
		}
	}
}
