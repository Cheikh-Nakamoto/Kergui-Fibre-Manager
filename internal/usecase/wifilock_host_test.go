package usecase_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// What the hosts of the closed Wi-Fi read (#107): the phone's core and, later,
// the relay serve the routes from these, on the F6600P mock and a fake clock.

// The preview names the relays it found, so the panel can say « relais
// inconnu » before a close that would leave the household's relay out.
func TestWiFiLock_PreviewNamesTheRelays(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	p, err := r.uc.Preview(ctx, usecase.WiFiCloseInput{MACFilterInput: r.in})
	if err != nil {
		t.Fatal(err)
	}
	if got := macStrings(p.Relays); !slices.Equal(got, []string{lkRelay}) {
		t.Fatalf("relays = %v, want %s", got, lkRelay)
	}

	r.household = lkHousehold{owners: r.household.owners}
	r.uc = r.build()
	p, err = r.uc.Preview(ctx, usecase.WiFiCloseInput{MACFilterInput: r.in})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Relays) != 0 || p.Self.String() != lkPhone {
		t.Fatalf("preview without a known relay = relays %v self %s", p.Relays, p.Self)
	}
}

// TickIfDue is what every device listing runs (the watch service's round,
// the app back in the foreground): it reaches the box only once an add
// window is past its deadline, and then closes the Wi-Fi again.
func TestWiFiLock_TickIfDueOnlyAtTheDeadline(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()

	logins := r.box.Logins()
	if tick, err := r.uc.TickIfDue(ctx, r.in); err != nil || tick != (usecase.WiFiTick{}) || r.box.Logins() != logins {
		t.Fatalf("tick on a Wi-Fi never closed = %+v, %v, %d logins", tick, err, r.box.Logins()-logins)
	}
	r.close(t)
	logins = r.box.Logins()
	r.clock.advance(time.Hour)
	if tick, err := r.uc.TickIfDue(ctx, r.in); err != nil || tick != (usecase.WiFiTick{}) || r.box.Logins() != logins {
		t.Fatalf("tick on a closed Wi-Fi = %+v, %v, %d logins; want nothing asked of the box", tick, err, r.box.Logins()-logins)
	}

	if _, err := r.uc.OpenWindow(ctx, r.in); err != nil {
		t.Fatal(err)
	}
	r.clock.advance(4 * time.Minute)
	writes := r.box.WiFiWrites()
	if tick, err := r.uc.TickIfDue(ctx, r.in); err != nil || tick.Reclosed || r.box.WiFiWrites() != writes {
		t.Fatalf("tick before the deadline = %+v, %v", tick, err)
	}
	r.clock.advance(2 * time.Minute)
	tick, err := r.uc.TickIfDue(ctx, r.in)
	if err != nil || !tick.Reclosed {
		t.Fatalf("tick past the deadline = %+v, %v; want closed again", tick, err)
	}
	if got := r.modes(lkAP1, lkAP5); !slices.Equal(got, []string{"Allow", "Allow"}) {
		t.Fatalf("modes = %v", got)
	}
	r.noHazard(t)
}

// The status says what the household asked besides what the box says: the
// intent, since when, and the devices « déconnectés du Wi-Fi ».
func TestWiFiLock_StatusCarriesTheIntentAndTheCutDevices(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	st, err := r.uc.Status(ctx, r.in)
	if err != nil || st.Closed || !st.Since.IsZero() || len(st.Cut) != 0 {
		t.Fatalf("status of a Wi-Fi never closed = %+v, %v", st, err)
	}
	if err := r.trust(t, lkTablet, domain.TrustBlocked, true); err != nil {
		t.Fatal(err)
	}
	r.clock.advance(time.Minute)
	r.close(t)
	st, err = r.uc.Status(ctx, r.in)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Closed || !st.Since.Equal(lockT0.Add(time.Minute)) || st.State != usecase.WiFiStateClosed {
		t.Fatalf("status = %+v, want closed since the close", st)
	}
	if got := macStrings(st.Cut); !slices.Equal(got, []string{lkTablet}) {
		t.Fatalf("cut = %v, want %s", got, lkTablet)
	}
	r.noHazard(t)
}

// Tracks tells a host whether a device's new state is the closed Wi-Fi's
// business: once the household closed it, or for a device « déconnecté du
// Wi-Fi ». Otherwise the host keeps its own path. It never reaches the box.
func TestWiFiLock_Tracks(t *testing.T) {
	r := newLockRig(t)
	ctx := context.Background()
	tablet, laptop := domain.MustMAC(lkTablet), domain.MustMAC(lkLaptop)
	logins := r.box.Logins()
	if on, err := r.uc.Tracks(ctx, r.in.BaseURL, laptop); err != nil || on {
		t.Fatalf("Tracks on a Wi-Fi never closed = %v, %v", on, err)
	}
	if r.box.Logins() != logins {
		t.Fatal("Tracks reached the box")
	}
	if err := r.trust(t, lkTablet, domain.TrustBlocked, true); err != nil {
		t.Fatal(err)
	}
	if on, _ := r.uc.Tracks(ctx, r.in.BaseURL, tablet); !on {
		t.Error("a device « déconnecté du Wi-Fi » is not tracked")
	}
	if on, _ := r.uc.Tracks(ctx, r.in.BaseURL, laptop); on {
		t.Error("another device is tracked while the Wi-Fi is open")
	}
	r.close(t)
	if on, _ := r.uc.Tracks(ctx, r.in.BaseURL, laptop); !on {
		t.Error("a device is not tracked while the Wi-Fi is closed")
	}
}
