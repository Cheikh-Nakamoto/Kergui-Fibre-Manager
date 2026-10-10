package zte_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/zte"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef660"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/ztef680"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

func newGateway(t *testing.T) (*zte.Gateway, func()) {
	t.Helper()
	srv := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	gw, err := zte.New(ztef660.Profile(), srv.URL, port.RouterOptions{})
	if err != nil {
		srv.Close()
		t.Fatalf("new gateway: %v", err)
	}
	return gw, srv.Close
}

func TestGateway_LoginSuccessAndReads(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()

	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatalf("login: %v", err)
	}

	info, err := gw.RouterInfo(ctx)
	if err != nil || info.Model != "ZXHN F660" || info.Firmware != "V6.0.10P2T2" {
		t.Fatalf("router info = (%+v, %v)", info, err)
	}

	devs, err := gw.Devices(ctx)
	if err != nil {
		t.Fatalf("devices: %v", err)
	}
	if len(devs) != 4 {
		t.Fatalf("want 4 devices, got %d", len(devs))
	}
	blocked := map[string]bool{}
	for _, d := range devs {
		blocked[d.MAC.String()] = d.Blocked
	}
	if !blocked["ac:bb:cc:00:00:33"] {
		t.Error("TV (ac:bb:cc:00:00:33) should be flagged blocked from the ACL")
	}
	if blocked["ac:bb:cc:00:00:11"] {
		t.Error("phone should not be blocked")
	}

	rules, err := gw.AccessRules(ctx)
	if err != nil || len(rules) != 2 {
		t.Fatalf("access rules = (%d, %v)", len(rules), err)
	}
}

func TestGateway_LoginFailure(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()
	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "wrong"}); !errors.Is(err, domain.ErrAuthFailed) {
		t.Fatalf("login err = %v, want ErrAuthFailed", err)
	}
}

func TestGateway_ReadWithoutSession(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()
	// No login performed → the mock rejects the page with 401.
	if _, err := gw.AccessRules(ctx); !errors.Is(err, domain.ErrSessionExpired) {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
}

func TestGateway_BlockUnblock(t *testing.T) {
	ctx := context.Background()
	gw, done := newGateway(t)
	defer done()
	if err := gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatalf("login: %v", err)
	}
	mac := domain.MustMAC("ac:bb:cc:00:00:11") // the phone, initially not blocked

	if err := gw.Block(ctx, mac); err != nil {
		t.Fatalf("block: %v", err)
	}
	if !aclHas(t, ctx, gw, mac) {
		t.Fatal("MAC should be in the ACL after block")
	}
	devs, _ := gw.Devices(ctx)
	if !deviceBlocked(devs, mac) {
		t.Error("device should be flagged blocked after block")
	}

	if err := gw.Unblock(ctx, mac); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if aclHas(t, ctx, gw, mac) {
		t.Fatal("MAC should be gone from the ACL after unblock")
	}
}

// TestGateway_SkeletonWriteNotImplemented confirms a model without a wired write
// path refuses writes rather than firing an unknown request.
func TestGateway_SkeletonWriteNotImplemented(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	defer srv.Close()
	gw, err := zte.New(ztef680.Profile(), srv.URL, port.RouterOptions{}) // F680 skeleton
	if err != nil {
		t.Fatal(err)
	}
	if err := gw.Block(ctx, domain.MustMAC("ac:bb:cc:00:00:11")); !errors.Is(err, domain.ErrNotImplemented) {
		t.Errorf("skeleton Block err = %v, want ErrNotImplemented", err)
	}
}

func aclHas(t *testing.T, ctx context.Context, gw *zte.Gateway, mac domain.MAC) bool {
	t.Helper()
	rules, err := gw.AccessRules(ctx)
	if err != nil {
		t.Fatalf("access rules: %v", err)
	}
	for _, r := range rules {
		if r.MAC.Equal(mac) {
			return true
		}
	}
	return false
}

func deviceBlocked(devs []domain.Device, mac domain.MAC) bool {
	for _, d := range devs {
		if d.MAC.Equal(mac) {
			return d.Blocked
		}
	}
	return false
}

// TestGateway_BlockRejectedWhenACLUnchanged confirms read-after-write
// verification: when the router responds 200 to a block POST but the ACL page
// still shows no rule, the gateway returns ErrBlockFailed rather than a
// misleading success.
func TestGateway_BlockRejectedWhenACLUnchanged(t *testing.T) {
	// Build a minimal httptest server that always returns 200 for the write,
	// but serves an empty ACL for the read-back.
	p := ztef660.Profile()
	mux := http.NewServeMux()

	// Login: always succeed (set cookie).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fake", Path: "/"})
			w.WriteHeader(200)
			return
		}
		// Login page with token.
		w.Write([]byte(`<html><input name="Frm_Logintoken" value="tok"></html>`))
	})
	// Write endpoint: always 200 but does NOT actually add the rule.
	mux.HandleFunc("/setpage.gch", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	// Read endpoint: always returns an empty ACL.
	mux.HandleFunc("/getpage.gch", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><script>
var _acl_mode = "black";
var _acl_num = 0;
</script></html>`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	gw, err := zte.New(p, srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"})

	err = gw.Block(ctx, domain.MustMAC("ac:bb:cc:00:00:11"))
	if !errors.Is(err, domain.ErrBlockFailed) {
		t.Fatalf("got %v, want ErrBlockFailed", err)
	}
}

// TestGateway_WriteTokenPostedWhenProfileDeclaresIt confirms that when a profile
// declares WriteTokenPage/WriteTokenField, the gateway scrapes and posts the
// token; and refuses to post when the token is absent.
func TestGateway_WriteTokenPostedWhenProfileDeclaresIt(t *testing.T) {
	p := ztef660.Profile()
	p.WriteTokenPage = p.ACLPage
	p.WriteTokenField = "Btn_token"

	var postedToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fake", Path: "/"})
			w.WriteHeader(200)
			return
		}
		w.Write([]byte(`<html><input name="Frm_Logintoken" value="tok"></html>`))
	})
	mux.HandleFunc("/getpage.gch", func(w http.ResponseWriter, r *http.Request) {
		// ACL page with a write token embedded.
		w.Write([]byte(`<html><script>
var _acl_mode = "black";
var _acl_num = 1;
var _acl_0 = new Array("ac:bb:cc:00:00:11","block");
</script><input name="Btn_token" value="csrf123"></html>`))
	})
	mux.HandleFunc("/setpage.gch", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		postedToken = r.PostForm.Get("Btn_token")
		w.WriteHeader(200)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	gw, err := zte.New(p, srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"})

	// Block should succeed (ACL already contains the MAC) and post the token.
	if err := gw.Block(ctx, domain.MustMAC("ac:bb:cc:00:00:11")); err != nil {
		t.Fatalf("block with token: %v", err)
	}
	if postedToken != "csrf123" {
		t.Errorf("posted token = %q, want %q", postedToken, "csrf123")
	}

	// Now test the case where the token is missing from the page.
	p2 := ztef660.Profile()
	p2.WriteTokenPage = p2.ACLPage
	p2.WriteTokenField = "Missing_token"

	mux2 := http.NewServeMux()
	mux2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fake", Path: "/"})
			w.WriteHeader(200)
			return
		}
		w.Write([]byte(`<html><input name="Frm_Logintoken" value="tok"></html>`))
	})
	mux2.HandleFunc("/getpage.gch", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><script>var _acl_mode = "black"; var _acl_num = 0;</script></html>`))
	})

	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()

	gw2, _ := zte.New(p2, srv2.URL, port.RouterOptions{})
	_ = gw2.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"})

	err = gw2.Block(ctx, domain.MustMAC("ac:bb:cc:00:00:11"))
	if !errors.Is(err, domain.ErrUnexpectedResponse) {
		t.Fatalf("block without token: got %v, want ErrUnexpectedResponse", err)
	}
}

// TestGateway_SkipWriteVerifyOption confirms that SkipWriteVerify bypasses
// read-after-write, so a 200 response is accepted even when the ACL is empty.
func TestGateway_SkipWriteVerifyOption(t *testing.T) {
	p := ztef660.Profile()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fake", Path: "/"})
			w.WriteHeader(200)
			return
		}
		w.Write([]byte(`<html><input name="Frm_Logintoken" value="tok"></html>`))
	})
	mux.HandleFunc("/setpage.gch", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})
	mux.HandleFunc("/getpage.gch", func(w http.ResponseWriter, r *http.Request) {
		// Empty ACL — without SkipWriteVerify this would fail.
		w.Write([]byte(`<html><script>var _acl_mode = "black"; var _acl_num = 0;</script></html>`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	gw, err := zte.New(p, srv.URL, port.RouterOptions{SkipWriteVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = gw.Login(ctx, domain.Credentials{Username: "admin", Password: "admin"})

	if err := gw.Block(ctx, domain.MustMAC("ac:bb:cc:00:00:11")); err != nil {
		t.Fatalf("block with SkipWriteVerify: %v", err)
	}
}
