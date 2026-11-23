package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	mathrand "math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin"
)

// TestE2E_Releases is issue #71, the hub's side of the website APK's
// in-app updates (#65): CI declares a build with its token, the build waits
// until a platform administrator publishes it, the app's check offers the
// newest published one, a withdrawal falls back to the previous one, the
// minimum is refused for a build nobody can download and then reaches the
// app, and the back-office counts the phones per version.
func TestE2E_Releases(t *testing.T) {
	local := fmt.Sprintf("78%07d", mathrand.IntN(10_000_000))
	w := newWorldWith(t, fastTimings, withPhoneOwner("+221"+local))
	ctx := context.Background()

	// The releases are global to the hub, not per household: start from
	// none, so earlier runs on the shared database do not answer for this one.
	for _, q := range []string{
		`DELETE FROM hub_releases WHERE platform = 'android' AND channel = 'website'`,
		`DELETE FROM hub_release_minimums WHERE platform = 'android' AND channel = 'website'`,
		`DELETE FROM hub_release_installs WHERE platform = 'android' AND channel = 'website'`,
	} {
		if _, err := e2ePool(t).Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	ci := &releaseClient{t: t, hub: w.hub.URL}
	build := func(n int) map[string]any {
		return map[string]any{
			"platform": "android", "channel": "website",
			"versionName": fmt.Sprintf("0.%d.0", n), "build": n,
			"url":    fmt.Sprintf("https://github.com/example/kergui/releases/download/v0.%d.0/kergui.apk", n),
			"sha256": strings.Repeat(fmt.Sprintf("%02x", n), 32), "size": 40_000_000 + n,
			"notes": "Corrections.",
		}
	}

	// CI without its token, or with a wrong one, declares nothing.
	if code, _ := ci.call(http.MethodPost, "/releases/v1/declare", "", build(20)); code != http.StatusUnauthorized {
		t.Fatalf("declare without a token = %d, want 401", code)
	}
	if code, _ := ci.call(http.MethodPost, "/releases/v1/declare", "not-the-token-not-the-token-0000", build(20)); code != http.StatusUnauthorized {
		t.Fatalf("declare with a wrong token = %d, want 401", code)
	}
	// A malformed declaration is refused.
	bad := build(20)
	bad["sha256"] = "abc"
	if code, _ := ci.call(http.MethodPost, "/releases/v1/declare", releasesToken, bad); code != http.StatusBadRequest {
		t.Fatalf("declare with a bad SHA-256 = %d, want 400", code)
	}

	// Two builds declared; the second twice, as a re-run release job would.
	for _, n := range []int{20, 21} {
		if code, body := ci.call(http.MethodPost, "/releases/v1/declare", releasesToken, build(n)); code != http.StatusCreated || str(body, "state") != "pending" {
			t.Fatalf("declare %d = %d %v, want 201 pending", n, code, body)
		}
	}
	if code, body := ci.call(http.MethodPost, "/releases/v1/declare", releasesToken, build(21)); code != http.StatusOK {
		t.Fatalf("declare 21 again = %d %v, want 200", code, body)
	}
	if n := countRows(t, `SELECT count(*) FROM hub_releases WHERE platform = 'android' AND channel = 'website' AND build = 21`); n != 1 {
		t.Fatalf("build 21 is stored %d times, want once", n)
	}

	// The app's check validates what it is sent.
	for _, bad := range []map[string]any{
		{"platform": "android", "channel": "website", "build": "19", "installationId": "install-aaaaaaaa"},
		{"platform": "android", "channel": "website", "build": 19, "installationId": strings.Repeat("x", 200)},
		{"platform": "android", "channel": "website", "build": 19, "installationId": "a b"},
		{"platform": "ios", "channel": "website", "build": 19, "installationId": "install-aaaaaaaa"},
	} {
		if code, _ := ci.call(http.MethodPost, "/releases/v1/check", "", bad); code != http.StatusBadRequest {
			t.Fatalf("check %v = %d, want 400", bad, code)
		}
	}
	// Pending builds are offered to nobody.
	check := func(install string, b int) map[string]any {
		t.Helper()
		code, body := ci.call(http.MethodPost, "/releases/v1/check", "", map[string]any{
			"platform": "android", "channel": "website", "build": b, "installationId": install,
		})
		if code != http.StatusOK {
			t.Fatalf("check %s@%d = %d %v", install, b, code, body)
		}
		return body
	}
	offered := func(body map[string]any) float64 {
		u, _ := body["update"].(map[string]any)
		if u == nil {
			return 0
		}
		n, _ := u["build"].(float64)
		return n
	}
	if got := check("install-a-"+local, 19); offered(got) != 0 || got["minimumBuild"] != float64(0) || got["required"] != false {
		t.Fatalf("check with only pending builds = %v, want no update and no minimum", got)
	}

	// A household owner is no administrator.
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/releases"},
		{http.MethodPost, "/api/v1/admin/releases/00000000-0000-0000-0000-000000000000/publish"},
		{http.MethodPost, "/api/v1/admin/releases/00000000-0000-0000-0000-000000000000/withdraw"},
		{http.MethodPut, "/api/v1/admin/releases/00000000-0000-0000-0000-000000000000/notes"},
		{http.MethodPut, "/api/v1/admin/releases/minimum"},
	} {
		if code, _ := w.owner.call(r.method, r.path, map[string]any{"notes": "x", "build": 20}); code != http.StatusForbidden {
			t.Fatalf("%s %s as an owner = %d, want 403", r.method, r.path, code)
		}
	}

	ops := releasesAdmin(t, w, local)
	ids := map[float64]string{}
	list := func() map[string]any {
		t.Helper()
		code, body := ops.call(http.MethodGet, "/api/v1/admin/releases", nil)
		if code != http.StatusOK {
			t.Fatalf("list = %d %v", code, body)
		}
		for _, r := range body["releases"].([]any) {
			m := r.(map[string]any)
			ids[m["build"].(float64)] = str(m, "id")
		}
		return body
	}
	if got := list(); len(got["releases"].([]any)) != 2 || ids[20] == "" || ids[21] == "" {
		t.Fatalf("list = %v, want builds 20 and 21", got)
	}

	// The minimum cannot point at a build nobody can download.
	if code, body := ops.call(http.MethodPut, "/api/v1/admin/releases/minimum", map[string]any{
		"platform": "android", "channel": "website", "build": 20,
	}); code != http.StatusConflict || releaseErrCode(body) != "MINIMUM_NOT_PUBLISHED" {
		t.Fatalf("minimum on a pending build = %d %v, want 409 MINIMUM_NOT_PUBLISHED", code, body)
	}

	// Published, 20 then 21: the newest is offered.
	for _, n := range []float64{20, 21} {
		if code, body := ops.call(http.MethodPost, "/api/v1/admin/releases/"+ids[n]+"/publish", nil); code != http.StatusOK || str(body, "state") != "published" {
			t.Fatalf("publish %v = %d %v", n, code, body)
		}
	}
	if got := check("install-a-"+local, 19); offered(got) != 21 {
		t.Fatalf("check after publishing = %v, want build 21", got)
	}
	if u := check("install-a-"+local, 19)["update"].(map[string]any); str(u, "url") == "" || str(u, "sha256") == "" || u["size"] == nil || str(u, "versionName") != "0.21.0" {
		t.Fatalf("offered update = %v, want its URL, SHA-256, size and name", u)
	}
	// A phone already on the newest build is offered nothing.
	if got := check("install-b-"+local, 21); offered(got) != 0 {
		t.Fatalf("check on the newest build = %v, want no update", got)
	}

	// Notes edited in the back-office reach the app.
	if code, body := ops.call(http.MethodPut, "/api/v1/admin/releases/"+ids[21]+"/notes", map[string]any{
		"notes": "L'onglet Appareils est plus rapide.",
	}); code != http.StatusOK || str(body, "notes") != "L'onglet Appareils est plus rapide." {
		t.Fatalf("edit notes = %d %v", code, body)
	}
	if u := check("install-a-"+local, 19)["update"].(map[string]any); str(u, "notes") != "L'onglet Appareils est plus rapide." {
		t.Fatalf("offered notes = %v", u["notes"])
	}

	// Withdrawn, 21 gives way to 20.
	if code, body := ops.call(http.MethodPost, "/api/v1/admin/releases/"+ids[21]+"/withdraw", nil); code != http.StatusOK || str(body, "state") != "withdrawn" {
		t.Fatalf("withdraw 21 = %d %v", code, body)
	}
	if got := check("install-a-"+local, 19); offered(got) != 20 {
		t.Fatalf("check after the withdrawal = %v, want build 20", got)
	}
	// A withdrawn build cannot be withdrawn again, nor the minimum set on it.
	if code, _ := ops.call(http.MethodPost, "/api/v1/admin/releases/"+ids[21]+"/withdraw", nil); code != http.StatusConflict {
		t.Fatalf("withdraw 21 twice = %d, want 409", code)
	}
	if code, _ := ops.call(http.MethodPut, "/api/v1/admin/releases/minimum", map[string]any{
		"platform": "android", "channel": "website", "build": 21,
	}); code != http.StatusConflict {
		t.Fatalf("minimum on a withdrawn build = %d, want 409", code)
	}

	// The minimum on a published build reaches the app.
	if code, body := ops.call(http.MethodPut, "/api/v1/admin/releases/minimum", map[string]any{
		"platform": "android", "channel": "website", "build": 20,
	}); code != http.StatusOK || body["minimumBuild"] != float64(20) {
		t.Fatalf("minimum 20 = %d %v", code, body)
	}
	if got := check("install-c-"+local, 18); got["minimumBuild"] != float64(20) || got["required"] != true || offered(got) != 20 {
		t.Fatalf("check below the minimum = %v, want build 20 required", got)
	}
	if got := check("install-b-"+local, 21); got["minimumBuild"] != float64(20) || got["required"] != false {
		t.Fatalf("check above the minimum = %v, want not required", got)
	}
	// The build the minimum stands on cannot be withdrawn: phones below it
	// would be blocked with nothing to download.
	if code, body := ops.call(http.MethodPost, "/api/v1/admin/releases/"+ids[20]+"/withdraw", nil); code != http.StatusConflict || releaseErrCode(body) != "MINIMUM_UNCOVERED" {
		t.Fatalf("withdraw the minimum's build = %d %v, want 409 MINIMUM_UNCOVERED", code, body)
	}

	// The counts follow the last build each installation reported: a moved
	// from 19 to 20, b stays on 21, c on 18.
	check("install-a-"+local, 20)
	got := list()
	if got["minimumBuild"] != float64(20) {
		t.Fatalf("list minimum = %v, want 20", got["minimumBuild"])
	}
	counts := map[float64]float64{}
	for _, v := range got["versions"].([]any) {
		m := v.(map[string]any)
		counts[m["build"].(float64)] = m["installs"].(float64)
	}
	if want := map[float64]float64{18: 1, 20: 1, 21: 1}; fmt.Sprint(counts) != fmt.Sprint(want) {
		t.Fatalf("installs per build = %v, want %v", counts, want)
	}
	for _, r := range got["releases"].([]any) {
		m := r.(map[string]any)
		if b := m["build"].(float64); m["installs"] != counts[b] {
			t.Fatalf("release %v installs = %v, want %v", b, m["installs"], counts[b])
		}
	}

	// Every back-office write is in the audit log (through the outbox).
	deadline := time.Now().Add(10 * time.Second)
	for {
		n := countRows(t, `SELECT count(*) FROM audit_logs WHERE actor_id = $1 AND action IN
			('RELEASE_PUBLISHED', 'RELEASE_WITHDRAWN', 'RELEASE_NOTES_EDITED', 'RELEASE_MINIMUM_SET')`, ops.userID)
		if n == 5 { // two publishes, a withdrawal, the notes, the minimum
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d audited release actions, want 5", n)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// releasesToken is the CI's declare token on the bench.
const releasesToken = "e2e-releases-token-0000000000000001"

// releasesAdmin creates a platform administrator and signs it in.
func releasesAdmin(t *testing.T, w *world, local string) *ownerApp {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	email, password := "releases-"+local+"@kergui.test", "a-long-admin-password"
	if _, err := admin.CreateAdmin(ctx, pool, email, password); err != nil {
		t.Fatal(err)
	}
	ops := &ownerApp{t: t, hub: w.hub}
	code, body := ops.call(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if code != http.StatusOK {
		t.Fatalf("admin login = %d %v", code, body)
	}
	ops.token = str(body, "accessToken")
	if code, body = ops.call(http.MethodGet, "/api/v1/admin/me", nil); code != http.StatusOK {
		t.Fatalf("me = %d %v", code, body)
	}
	ops.userID = str(body, "userId")
	return ops
}

// releaseClient calls the /releases/v1 routes, as CI and the app do, each
// call from its own address so the test stays inside the route's budget.
type releaseClient struct {
	t   *testing.T
	hub string
}

var releaseCallerIP atomic.Uint32

func (c *releaseClient) call(method, path, token string, body any) (int, map[string]any) {
	c.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, c.hub+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	n := releaseCallerIP.Add(1)
	req.Header.Set("CF-Connecting-IP", fmt.Sprintf("10.71.%d.%d", n/250, n%250+1))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// releaseErrCode is the hub's error code in an error body {"error":{"code":…}}.
func releaseErrCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}
