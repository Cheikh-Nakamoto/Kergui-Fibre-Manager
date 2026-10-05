package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/controller/httpapi"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/discovery"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/sqlite"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/persistence/vault"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/router/catalog"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/adapter/vendor"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/clock"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/infra/logging"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

func newAPI(t *testing.T) *httptest.Server {
	t.Helper()
	router := httptest.NewServer(mockrouter.New("admin", "admin").Handler())
	t.Cleanup(router.Close)

	factory := catalog.New()
	clk := clock.Real{}
	disco := discovery.New(factory, clk)
	db, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	vlt := vault.New(sqlite.NewCredentialStore(db), "test-key")
	// Pre-store credentials as `login` would.
	if err := vlt.Store(context.Background(), usecase.RouterID(router.URL), domain.Credentials{Username: "admin", Password: "admin"}); err != nil {
		t.Fatal(err)
	}
	logger := logging.New(io.Discard, false)
	devRepo := sqlite.NewDeviceRepo(db)

	api := httpapi.New(httpapi.Services{
		Discover: usecase.NewDiscoverRouter(disco),
		List:     usecase.NewListDevices(factory, vlt, devRepo, vendor.New(nil), disco, clk, logger),
		Inspect:  usecase.NewInspectRouter(factory, disco, vlt, clk, logger),
		Block:    usecase.NewBlockDevice(factory, vlt, disco, clk, logger),
		Unblock:  usecase.NewUnblockDevice(factory, vlt, disco, clk, logger),
		Auth:     usecase.NewAuthenticate(factory, vlt, sqlite.NewRouterRepo(db), disco, clk, logger),
		Rename:   usecase.NewRenameDevice(devRepo),
	}, httpapi.Config{BaseURL: router.URL, Version: "test"})

	apiSrv := httptest.NewServer(api.Handler())
	t.Cleanup(apiSrv.Close)
	return apiSrv
}

func TestAPI_HealthAndDevices(t *testing.T) {
	api := newAPI(t)

	// health
	if code, _ := getJSON(t, api.URL+"/api/health"); code != 200 {
		t.Fatalf("health code = %d", code)
	}

	// discover — domain.RouterInfo marshals AdapterID as "AdapterID"
	code, body := getJSON(t, api.URL+"/api/discover")
	if code != 200 {
		t.Fatalf("discover code = %d", code)
	}
	if body["AdapterID"] != "zte_f660" {
		t.Errorf("discover AdapterID = %v, want zte_f660", body["AdapterID"])
	}

	// devices
	resp, err := http.Get(api.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("devices code = %d", resp.StatusCode)
	}
	var devs []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&devs); err != nil {
		t.Fatal(err)
	}
	if len(devs) != 4 {
		t.Fatalf("want 4 devices, got %d", len(devs))
	}
}

func TestAPI_BlockReturns501(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Post(api.URL+"/api/devices/ac:bb:cc:00:00:11/block", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("block code = %d, want 501 (write path not yet verified)", resp.StatusCode)
	}
}

func TestAPI_Login(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Post(api.URL+"/api/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"admin","test":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login test code = %d", resp.StatusCode)
	}
}

func TestAPI_Rename(t *testing.T) {
	api := newAPI(t)
	// Populate the inventory first.
	if r, _ := http.Get(api.URL + "/api/devices"); r != nil {
		r.Body.Close()
	}
	req, _ := http.NewRequest(http.MethodPatch, api.URL+"/api/devices/ac:bb:cc:00:00:11",
		strings.NewReader(`{"custom_name":"Téléphone Maman"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("rename code = %d", resp.StatusCode)
	}
	// Confirm it stuck.
	resp2, _ := http.Get(api.URL + "/api/devices")
	defer resp2.Body.Close()
	var devs []map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&devs)
	found := false
	for _, d := range devs {
		if d["MAC"] == "ac:bb:cc:00:00:11" && d["CustomName"] == "Téléphone Maman" {
			found = true
		}
	}
	if !found {
		t.Error("custom name not persisted")
	}
}

func TestAPI_ServesDashboard(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Get(api.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "MON RÉSEAU") {
		t.Fatalf("dashboard not served (code %d)", resp.StatusCode)
	}
}

func TestAPI_InvalidMAC(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Post(api.URL+"/api/devices/not-a-mac/block", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid mac code = %d, want 400", resp.StatusCode)
	}
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}
