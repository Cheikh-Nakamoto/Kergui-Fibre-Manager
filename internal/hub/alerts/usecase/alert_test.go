package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// App push (issue #49): a closed inventory window reaches every installation
// of the household that registered a token, whether or not its member opted
// in to WhatsApp, and carries what the app needs to open the device sheet.

var t0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	userOwn = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	userAwa = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

// fakeRepo is the slice of the repository the alert path uses.
type fakeRepo struct {
	domain.Repository
	mu         sync.Mutex
	alerts     map[string]domain.Alert
	optIns     []domain.OptIn
	deliveries []domain.Delivery
}

func (r *fakeRepo) CreateAlert(_ context.Context, a domain.Alert) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.alerts == nil {
		r.alerts = map[string]domain.Alert{}
	}
	if _, ok := r.alerts[a.WindowID]; ok {
		return false, nil
	}
	a.ID = "alert-" + a.WindowID
	r.alerts[a.WindowID] = a
	return true, nil
}

func (r *fakeRepo) AlertByWindow(_ context.Context, _, windowID string) (domain.Alert, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.alerts[windowID], nil
}

func (r *fakeRepo) ActiveOptIns(context.Context, string) ([]domain.OptIn, error) {
	return r.optIns, nil
}

func (r *fakeRepo) RecordDelivery(_ context.Context, d domain.Delivery) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveries = append(r.deliveries, d)
	return nil
}

type fakeNotifier struct {
	domain.Notifier
	sent []domain.Phone
}

func (n *fakeNotifier) Name() string { return "fake" }
func (n *fakeNotifier) Send(_ context.Context, to domain.Phone, _ domain.OutboundMessage) (string, error) {
	n.sent = append(n.sent, to)
	return "msg-1", nil
}

// fakePusher is the push adapter's fake: it records, and answers "gone"
// for the tokens told to.
type fakePusher struct {
	pushed []domain.PushTarget
	msgs   []domain.PushMessage
	gone   map[string]bool
}

func (p *fakePusher) Name() string { return "fake-push" }
func (p *fakePusher) Push(_ context.Context, to domain.PushTarget, msg domain.PushMessage) error {
	if p.gone[to.Token] {
		return domain.ErrPushTokenGone
	}
	p.pushed = append(p.pushed, to)
	p.msgs = append(p.msgs, msg)
	return nil
}

type fakeTargets struct {
	targets   []domain.PushTarget
	forgotten []string
}

func (f *fakeTargets) For(_ context.Context, tenantID string) ([]domain.PushTarget, error) {
	var out []domain.PushTarget
	for _, t := range f.targets {
		if t.TenantID == tenantID {
			out = append(out, t)
		}
	}
	return out, nil
}
func (f *fakeTargets) Register(context.Context, domain.PushTarget, time.Time) error { return nil }
func (f *fakeTargets) Unregister(context.Context, string, string, string) error     { return nil }
func (f *fakeTargets) Forget(_ context.Context, token string) error {
	f.forgotten = append(f.forgotten, token)
	return nil
}

func firstSeen(window string, devices ...map[string]any) map[string]any {
	ds := make([]any, 0, len(devices))
	for _, d := range devices {
		ds = append(ds, d)
	}
	return map[string]any{"tenant_id": tenantA, "window_id": window, "opened_at": t0, "devices": ds}
}

func bench() (*fakeRepo, *fakeNotifier, *fakePusher, *fakeTargets, Alerts) {
	repo := &fakeRepo{optIns: []domain.OptIn{{TenantID: tenantA, UserID: userOwn, Phone: "+221771234567"}}}
	n := &fakeNotifier{}
	p := &fakePusher{gone: map[string]bool{}}
	tg := &fakeTargets{targets: []domain.PushTarget{
		{TenantID: tenantA, UserID: userOwn, Platform: domain.PlatformAndroid, Token: "ExponentPushToken[own]"},
		// Awa never linked WhatsApp: the app is her only channel.
		{TenantID: tenantA, UserID: userAwa, Platform: domain.PlatformIOS, Token: "ExponentPushToken[awa]"},
		{TenantID: "22222222-2222-2222-2222-222222222222", UserID: userOwn, Platform: domain.PlatformAndroid, Token: "ExponentPushToken[other-home]"},
	}}
	uc := Alerts{Repo: repo, Notifier: n, Pusher: p, Targets: tg, Now: func() time.Time { return t0 }}
	return repo, n, p, tg, uc
}

func TestAlerts_PushReachesEveryInstallationOfTheHousehold(t *testing.T) {
	repo, n, p, _, uc := bench()
	ev := firstSeen("w1",
		map[string]any{"no": 1, "mac": "aa:11:22:33:44:55", "device_id": "d1", "hostname": "TECNO-POP-10", "ip": "192.168.1.77"},
		map[string]any{"no": 2, "mac": "aa:11:22:33:44:66", "device_id": "d2"})
	if err := uc.HandleFirstSeen(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 1 {
		t.Fatalf("%d WhatsApp messages, want 1", len(n.sent))
	}
	if len(p.pushed) != 2 {
		t.Fatalf("pushed to %+v, want the household's two installations", p.pushed)
	}
	for _, m := range p.msgs {
		if m.Data["mac"] != "aa:11:22:33:44:55" || m.Data["alertId"] != "alert-w1" || m.Data["tenantId"] != tenantA {
			t.Errorf("push data = %v: the app could not open the device sheet", m.Data)
		}
		if m.Data["kind"] != "new_device" || m.Title == "" || m.Body == "" {
			t.Errorf("push = %+v", m)
		}
	}
	push := 0
	for _, d := range repo.deliveries {
		if d.Channel == "push" {
			push++
			if d.Provider != "fake-push" || d.Err != "" {
				t.Errorf("push delivery = %+v", d)
			}
		}
	}
	if push != 2 {
		t.Errorf("%d push deliveries recorded, want 2", push)
	}

	// The outbox delivers at least once: the second time pushes nothing.
	if err := uc.HandleFirstSeen(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if len(p.pushed) != 2 {
		t.Errorf("a redelivered event pushed again: %d", len(p.pushed))
	}
}

func TestAlerts_PushWithoutAnyWhatsAppOptIn(t *testing.T) {
	repo, n, p, _, uc := bench()
	repo.optIns = nil
	if err := uc.HandleFirstSeen(context.Background(), firstSeen("w2", map[string]any{"no": 1, "mac": "aa:00:00:00:00:01"})); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 0 || len(p.pushed) != 2 {
		t.Fatalf("WhatsApp %d, push %d: want 0 and 2", len(n.sent), len(p.pushed))
	}
}

func TestAlerts_AGoneTokenIsForgotten(t *testing.T) {
	repo, _, p, tg, uc := bench()
	p.gone["ExponentPushToken[awa]"] = true
	if err := uc.HandleFirstSeen(context.Background(), firstSeen("w3", map[string]any{"no": 1, "mac": "aa:00:00:00:00:02"})); err != nil {
		t.Fatal(err)
	}
	if len(tg.forgotten) != 1 || tg.forgotten[0] != "ExponentPushToken[awa]" {
		t.Fatalf("forgotten = %v", tg.forgotten)
	}
	if len(p.pushed) != 1 {
		t.Fatalf("pushed %d, want the one installation left", len(p.pushed))
	}
	failed := false
	for _, d := range repo.deliveries {
		if d.Channel == "push" && d.UserID == userAwa {
			failed = errors.Is(errors.New(d.Err), domain.ErrPushTokenGone) || d.Err != ""
		}
	}
	if !failed {
		t.Error("the failed push is not recorded for Awa")
	}
}
