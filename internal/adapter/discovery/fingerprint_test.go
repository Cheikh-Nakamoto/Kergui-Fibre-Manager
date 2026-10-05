package discovery_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Unix(0, 0) }

func TestDiscover_IdentifiesF660(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Server", "mini_httpd")
		_, _ = w.Write([]byte(fixtures.ZTEF660Login))
	}))
	defer srv.Close()

	d := discovery.New(catalog.New(), fixedClock{})
	info, err := d.Discover(context.Background(), srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !info.Reachable || info.AdapterID != "zte_f660" || info.Vendor != "ZTE" {
		t.Fatalf("info = %+v", info)
	}
	if info.Title != "ZXHN F660" || info.Confidence != 90 {
		t.Errorf("title/confidence = (%q, %d)", info.Title, info.Confidence)
	}
}

func TestDiscover_GenericZTEFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Router</title></head><body>ZXHN gateway, Frm_Logintoken</body></html>`))
	}))
	defer srv.Close()

	d := discovery.New(catalog.New(), fixedClock{})
	info, err := d.Discover(context.Background(), srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.Vendor != "ZTE" || info.AdapterID != "" {
		t.Errorf("generic fallback = %+v (want vendor ZTE, no adapter)", info)
	}
}

func TestDiscover_Unreachable(t *testing.T) {
	d := discovery.New(catalog.New(), fixedClock{})
	_, err := d.Discover(context.Background(), "http://127.0.0.1:1", port.RouterOptions{Timeout: 500 * time.Millisecond})
	if !errors.Is(err, domain.ErrRouterUnreachable) {
		t.Fatalf("err = %v, want ErrRouterUnreachable", err)
	}
}
