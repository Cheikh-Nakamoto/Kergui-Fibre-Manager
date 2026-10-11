package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

type portalClock struct{ t time.Time }

func (c portalClock) Now() time.Time { return c.t }

type quiet struct{}

func (quiet) Debugf(string, ...any) {}
func (quiet) Infof(string, ...any)  {}
func (quiet) Warnf(string, ...any)  {}
func (quiet) Errorf(string, ...any) {}

func TestPortal_RedirectsAndShowsOnlyOwnState(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	routerID := usecase.RouterID("http://r")
	mac := domain.MustMAC("aa:bb:cc:00:00:11")
	if _, err := sqlite.NewDeviceRepo(db).Upsert(ctx, domain.Device{ID: "x", RouterID: routerID, MAC: mac,
		IP: domain.MustIP("127.0.0.1"), Hostname: "Phone", Blocked: true, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	pays := sqlite.NewPaymentRepo(db)
	if err := pays.Create(ctx, domain.Payment{ID: "p", RouterID: routerID, Kind: domain.PaymentTest, MAC: mac,
		Period: "2026-10", Bytes: 2e9, AmountXOF: 1000, Status: domain.PaymentPending,
		CheckoutURL: "https://checkout.naboopay.com/p", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	c := usecase.NewCaptive(sqlite.NewDeviceRepo(db), pays, sqlite.NewCutRepo(db), sqlite.NewIdentityRepo(db),
		sqlite.NewReminderRepo(db), portalClock{now}, quiet{})
	if err := c.Refresh(ctx, "http://r"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpapi.PortalHandler(c, "http://192.168.1.8/portail", quiet{}))
	defer srv.Close()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, path := range []string{"/generate_204", "/hotspot-detect.html", "/api/devices", "/api/config/env", "/"} {
		resp, err := noFollow.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "http://192.168.1.8/portail" {
			t.Errorf("%s -> %d %q, want redirect to the portal", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}

	resp, err := http.Get(srv.URL + "/portail/api/me")
	if err != nil {
		t.Fatal(err)
	}
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	pay, _ := me["payment"].(map[string]any)
	if me["captive"] != true || me["name"] != "Phone" || pay["checkout_url"] != "https://checkout.naboopay.com/p" {
		t.Errorf("me = %v", me)
	}
	if _, leaked := me["mac"]; leaked {
		t.Error("portal must not echo identifiers beyond what the page needs")
	}

	resp, err = http.Get(srv.URL + "/portail")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("portal page: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
