package replyblock

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	alertsusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/usecase"
)

// One answer has one meaning, whatever the alert (audit C2): a number is the
// device to block, « OK » authorises. The bare « 2 » that used to authorise
// a one-device alert is still understood there, and nowhere else.

const (
	tenant = "11111111-1111-1111-1111-111111111111"
	owner  = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	phone  = alertsdomain.Phone("+221771234567")
)

var t0 = time.Date(2026, 11, 26, 21, 5, 0, 0, time.UTC)

func alertOf(n int) alertsdomain.Alert {
	a := alertsdomain.Alert{ID: "alert-1", TenantID: tenant, CreatedAt: t0, ReplyUntil: t0.Add(time.Hour)}
	for i := 1; i <= n; i++ {
		a.Devices = append(a.Devices, alertsdomain.AlertDevice{
			No: i, MAC: "aa:11:22:33:44:5" + string(rune('0'+i)), Hostname: "Galaxy-A1" + string(rune('0'+i)),
			IP: "192.168.1.7" + string(rune('0'+i)), AsOf: t0,
		})
	}
	return a
}

func TestDecide_ADigitAlwaysBlocksAndOKAlwaysAuthorises(t *testing.T) {
	for _, c := range []struct {
		name    string
		text    string
		devices int
		want    decision
	}{
		{"1 on one device", "1", 1, decision{block: []int{1}}},
		{"1 on two devices", "1", 2, decision{block: []int{1}}},
		{"2 on two devices blocks device 2", "2", 2, decision{block: []int{2}}},
		{"2 on three devices blocks device 2", "2", 3, decision{block: []int{2}}},
		{"1 2 on two devices", "1 2", 2, decision{block: []int{1, 2}}},
		{"OK on one device", "OK", 1, decision{approve: true}},
		{"OK on two devices", "ok", 2, decision{approve: true}},
		// The transition: « 2 » alone on a one-device alert, where it
		// cannot be a device number, is still the old « authorise ».
		{"2 alone on one device", "2", 1, decision{approve: true}},
		{"2 with another number on one device", "1 2", 1, decision{block: []int{1, 2}}},
	} {
		got := decide(alertsdomain.ReadIntent(c.text), alertOf(c.devices))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// fakeAlerts is the slice of the alerts repository a reply uses.
type fakeAlerts struct {
	alertsdomain.Repository
	alert alertsdomain.Alert
	err   error
}

func (f *fakeAlerts) AlertForReply(context.Context, alertsdomain.Phone, string, time.Time) (alertsdomain.Alert, error) {
	return f.alert, f.err
}

func (f *fakeAlerts) OptInsByPhone(context.Context, alertsdomain.Phone) ([]alertsdomain.OptIn, error) {
	return []alertsdomain.OptIn{{TenantID: tenant, UserID: owner, Phone: phone}}, nil
}

// service is a reply path without a hub key (blocking is off), with an
// approver that records what it approved.
func service(alerts *fakeAlerts) (*Service, *[]string) {
	var approved []string
	return New(Config{
		Alerts:  alerts,
		IsAdmin: func(context.Context, string, string) (bool, error) { return true, nil },
		Approve: func(_ context.Context, _ string, mac string, ok bool, _ string, _ time.Time) error {
			if ok {
				approved = append(approved, mac)
			}
			return nil
		},
		Now: func() time.Time { return t0.Add(time.Minute) },
	}), &approved
}

func reply(t *testing.T, s *Service, text string) string {
	t.Helper()
	answer, _ := s.Reply(context.Background(), alertsdomain.InboundMessage{From: phone, Text: text}, alertsdomain.ReadIntent(text))
	return answer
}

func TestReply_OKAuthorisesEveryDevice(t *testing.T) {
	for _, n := range []int{1, 2} {
		s, approved := service(&fakeAlerts{alert: alertOf(n)})
		answer := reply(t, s, "OK")
		if len(*approved) != n {
			t.Errorf("%d devices: approved %v", n, *approved)
		}
		if !strings.Contains(answer, "est autorisé") {
			t.Errorf("%d devices: answer %q", n, answer)
		}
	}
}

func TestReply_TheTransitionalTwoAuthorisesAOneDeviceAlert(t *testing.T) {
	s, approved := service(&fakeAlerts{alert: alertOf(1)})
	answer := reply(t, s, "2")
	if len(*approved) != 1 || !strings.Contains(answer, "est autorisé") {
		t.Fatalf("approved %v, answer %q", *approved, answer)
	}
}

func TestReply_TwoNeverAuthorisesAnAlertAboutSeveralDevices(t *testing.T) {
	s, approved := service(&fakeAlerts{alert: alertOf(2)})
	answer := reply(t, s, "2")
	if len(*approved) != 0 {
		t.Fatalf("« 2 » on a two-device alert approved %v", *approved)
	}
	// Blocking is off on this hub: the household is told so, in Kergui's
	// name, and nothing happened.
	if !strings.Contains(answer, "Kergui") || strings.Contains(answer, "hub") {
		t.Errorf("answer %q", answer)
	}
}

// TestReply_UnquotedWithSeveralOpenAlertsAsksWhichOne: « 1 » is the same
// question with blocking on, which the end-to-end test asks
// (TestE2E_UnquotedReplyWithTwoOpenAlertsAsks).
func TestReply_UnquotedWithSeveralOpenAlertsAsksWhichOne(t *testing.T) {
	for _, text := range []string{"2", "OK"} {
		s, approved := service(&fakeAlerts{err: alertsdomain.ErrAmbiguousAlert})
		answer, err := s.Reply(context.Background(), alertsdomain.InboundMessage{From: phone, Text: text}, alertsdomain.ReadIntent(text))
		if answer != alertsusecase.ComposeAmbiguous {
			t.Errorf("%q: answer %q, want the question", text, answer)
		}
		if !errors.Is(err, alertsdomain.ErrAmbiguousAlert) {
			t.Errorf("%q: err %v", text, err)
		}
		if len(*approved) != 0 {
			t.Errorf("%q: approved %v", text, *approved)
		}
	}
	if got := alertsusecase.ComposeAmbiguous; got != "Pour quelle alerte ? Répondez directement au message de l'alerte (appui long, puis Répondre)." {
		t.Errorf("question %q", got)
	}
}

func TestReply_NoAnswerSaysHub(t *testing.T) {
	s, _ := service(&fakeAlerts{alert: alertOf(1)})
	s.cfg.Approve = nil
	for _, text := range []string{"OK", "1"} {
		if answer := reply(t, s, text); strings.Contains(answer, "hub") || !strings.Contains(answer, "Kergui") {
			t.Errorf("%q: answer %q", text, answer)
		}
	}
}
