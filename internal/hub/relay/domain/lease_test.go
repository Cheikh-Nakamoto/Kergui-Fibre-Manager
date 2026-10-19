package domain

import (
	"testing"
	"time"

	agents "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
)

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const pin = "3f364506861835de605d766589afbd712b5ed154ad1ec10c07477a2c1633a1fc"

func network() agents.Network {
	return agents.Network{
		SSIDs:     []string{"MonWiFi-2G", "MonWiFi-5G"},
		BSSIDs:    []string{"aa:bb:cc:dd:ee:01"},
		RouterPin: pin,
	}
}

func TestEligibility(t *testing.T) {
	tests := []struct {
		name   string
		net    agents.Network
		report Report
		want   bool
	}{
		{
			name: "on the household Wi-Fi, in front of the household box",
			net:  network(), report: Report{SSID: "MonWiFi-2G", BSSID: "aa:bb:cc:dd:ee:01", RouterPin: pin},
			want: true,
		},
		{
			name: "a PC on Ethernet reports no Wi-Fi, so the pin alone decides",
			net:  network(), report: Report{RouterPin: pin},
			want: true,
		},
		{
			name: "another box answers on the same LAN address",
			net:  network(), report: Report{SSID: "MonWiFi-2G", RouterPin: "0000"},
			want: false,
		},
		{
			name: "the relay left for a neighbour's Wi-Fi",
			net:  network(), report: Report{SSID: "FreeWifi", RouterPin: pin},
			want: false,
		},
		{
			name: "the right SSID but an access point nobody registered",
			net:  network(), report: Report{SSID: "MonWiFi-2G", BSSID: "ff:ff:ff:ff:ff:ff", RouterPin: pin},
			want: false,
		},
		{
			name: "the relay reports no box identity at all",
			net:  network(), report: Report{SSID: "MonWiFi-2G"},
			want: false,
		},
		{
			name: "the household never registered its router",
			net:  agents.Network{}, report: Report{SSID: "MonWiFi-2G", RouterPin: pin},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := Eligibility(tc.net, tc.report)
			if ok != tc.want {
				t.Fatalf("Eligibility = %v (%s), want %v", ok, reason, tc.want)
			}
			if !ok && reason == "" {
				t.Error("a refusal must say why: the app shows this to the user")
			}
		})
	}
}

func eligible(id string, kind agents.Kind, owner bool, seen time.Time) Presence {
	return Presence{AgentID: id, Kind: kind, IsOwner: owner, Eligible: true, SeenAt: seen}
}

func TestDecide_GrantsAndRenews(t *testing.T) {
	me := eligible("a1", agents.KindPhone, true, now)

	out := Decide(Lease{}, me, nil, Report{}, now)
	if out.State != StateActive || !out.Grant || out.Epoch != 1 {
		t.Fatalf("a free lease was not granted: %+v", out)
	}
	if !out.ExpiresAt.Equal(now.Add(LeaseTTL)) {
		t.Errorf("ExpiresAt = %v, want now+%s", out.ExpiresAt, LeaseTTL)
	}

	held := Lease{AgentID: "a1", Epoch: 1, ExpiresAt: now.Add(40 * time.Second)}
	out = Decide(held, me, nil, Report{}, now.Add(20*time.Second))
	if out.State != StateActive || out.Grant || out.Epoch != 1 {
		t.Fatalf("the holder's heartbeat did not renew in place: %+v", out)
	}
}

func TestDecide_OnlyOneRelayIsActive(t *testing.T) {
	a := eligible("a1", agents.KindPhone, true, now)
	b := eligible("a2", agents.KindPhone, false, now)
	held := Lease{AgentID: "a1", Epoch: 1, ExpiresAt: now.Add(40 * time.Second)}

	if out := Decide(held, b, []Presence{a}, Report{}, now); out.State != StateStandby {
		t.Fatalf("a second relay was told it is active: %+v", out)
	}
}

// TestDecide_TakesOverWhenTheHolderStops is the failover rule: the second
// relay may not wait longer than the lease.
func TestDecide_TakesOverWhenTheHolderStops(t *testing.T) {
	dead := Lease{AgentID: "a1", Epoch: 4, ExpiresAt: now.Add(LeaseTTL)}
	b := eligible("a2", agents.KindPhone, false, now)

	// While the lease is alive, b waits — and is told to come back before
	// it ends, not a full heartbeat later.
	out := Decide(dead, b, nil, Report{}, now.Add(50*time.Second))
	if out.State != StateStandby {
		t.Fatalf("b took over a live lease: %+v", out)
	}
	if out.RetryAfter != 10*time.Second {
		t.Errorf("RetryAfter = %s, want the 10s left on the lease", out.RetryAfter)
	}

	// Once it lapses, b takes it with a new epoch, which fences the old
	// holder's results.
	out = Decide(dead, b, nil, Report{}, now.Add(LeaseTTL+time.Second))
	if out.State != StateActive || !out.Grant {
		t.Fatalf("b did not take over an expired lease: %+v", out)
	}
	if out.Epoch != 5 {
		t.Errorf("Epoch = %d, want 5", out.Epoch)
	}
}

func TestDecide_CleanShutdownReleasesAtOnce(t *testing.T) {
	a := eligible("a1", agents.KindPhone, true, now)
	held := Lease{AgentID: "a1", Epoch: 1, ExpiresAt: now.Add(LeaseTTL)}

	out := Decide(held, a, nil, Report{Released: true}, now)
	if out.State != StateStandby || !out.Release {
		t.Fatalf("a clean shutdown did not release the lease: %+v", out)
	}
	// The next relay then takes it immediately, not 60 s later.
	b := eligible("a2", agents.KindPhone, false, now)
	if out := Decide(Lease{Epoch: 1}, b, nil, Report{}, now); out.State != StateActive {
		t.Fatalf("b did not take the released lease: %+v", out)
	}
}

func TestDecide_PriorityHandsOver(t *testing.T) {
	phone := eligible("a1", agents.KindPhone, false, now)
	pc := eligible("a2", agents.KindPC, false, now)

	held := Lease{AgentID: "a1", Epoch: 1, ExpiresAt: now.Add(LeaseTTL)}
	out := Decide(held, phone, []Presence{pc}, Report{}, now)
	if out.State != StateStandby || !out.Release {
		t.Fatalf("the phone did not hand over to the dedicated relay: %+v", out)
	}
	// Only then does the PC take it: never two sessions at once.
	if out := Decide(Lease{Epoch: 1}, pc, []Presence{phone}, Report{}, now); out.State != StateActive {
		t.Fatalf("the PC did not take the released lease: %+v", out)
	}
}

func TestDecide_StalePresenceIsNotACandidate(t *testing.T) {
	phone := eligible("a1", agents.KindPhone, false, now)
	// A PC that stopped answering an hour ago must not keep the phone from
	// holding the lease.
	pc := eligible("a2", agents.KindPC, false, now.Add(-time.Hour))

	if out := Decide(Lease{}, phone, []Presence{pc}, Report{}, now); out.State != StateActive {
		t.Fatalf("a stale relay blocked a live one: %+v", out)
	}
}

func TestDecide_IneligibleRelayStaysInStandby(t *testing.T) {
	me := Presence{AgentID: "a1", Kind: agents.KindPhone, Eligible: false, Reason: "the relay is on another Wi-Fi network", SeenAt: now}

	out := Decide(Lease{}, me, nil, Report{}, now)
	if out.State != StateStandby || out.Grant {
		t.Fatalf("an ineligible relay was granted the lease: %+v", out)
	}
	if out.Reason == "" {
		t.Error("the standby carries no reason")
	}

	// It also loses a lease it already held: it may have left the house.
	held := Lease{AgentID: "a1", Epoch: 2, ExpiresAt: now.Add(LeaseTTL)}
	if out := Decide(held, me, nil, Report{}, now); !out.Release {
		t.Fatalf("a relay that left the network kept the lease: %+v", out)
	}
}

func TestPriority(t *testing.T) {
	ownerPhone := eligible("a1", agents.KindPhone, true, now)
	other := eligible("a2", agents.KindPhone, false, now)
	pc := eligible("a3", agents.KindPC, false, now)
	dedicated := eligible("a4", agents.KindDedicated, false, now)

	if !(Priority(dedicated) == Priority(pc) && Priority(pc) > Priority(ownerPhone) && Priority(ownerPhone) > Priority(other)) {
		t.Errorf("priority order is wrong: dedicated=%d pc=%d owner=%d other=%d",
			Priority(dedicated), Priority(pc), Priority(ownerPhone), Priority(other))
	}
	if Priority(Presence{}) != 0 {
		t.Error("an absent candidate must rank below every real one")
	}
}
