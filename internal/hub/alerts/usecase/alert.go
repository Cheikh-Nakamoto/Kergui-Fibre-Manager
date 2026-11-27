package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Logger is the slice of a logger this module uses.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Alerts turns a closed inventory window into messages.
//
// It is driven by the outbox, so it runs at least once per window and must
// be idempotent: the alert row is unique per window, and only the call that
// created it sends anything.
type Alerts struct {
	Repo     domain.Repository
	Notifier domain.Notifier
	Pusher   domain.Pusher
	Targets  domain.PushTargets
	Now      Clock
	Log      Logger
}

// FirstSeenEvent is the payload of router.device_first_seen, as the
// inventory module publishes it. It is the contract between the two
// modules; neither reads the other's tables.
type FirstSeenEvent struct {
	TenantID string               `json:"tenant_id"`
	WindowID string               `json:"window_id"`
	OpenedAt time.Time            `json:"opened_at"`
	Devices  []domain.AlertDevice `json:"devices"`
}

// HandleFirstSeen is the outbox handler. A payload it cannot use is a
// permanent failure, so it is logged and swallowed rather than retried ten
// times: the window is already closed, and nothing a retry does would make
// the payload parseable.
func (uc Alerts) HandleFirstSeen(ctx context.Context, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("alerts: re-encode event: %w", err)
	}
	var ev FirstSeenEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		if uc.Log != nil {
			uc.Log.Warn("alerts: unusable device_first_seen event", "error", err)
		}
		return nil
	}
	if ev.TenantID == "" || ev.WindowID == "" || len(ev.Devices) == 0 {
		if uc.Log != nil {
			uc.Log.Warn("alerts: device_first_seen event names no household or no device")
		}
		return nil
	}

	now := uc.Now().UTC()
	devices := append([]domain.AlertDevice(nil), ev.Devices...)
	sort.Slice(devices, func(i, j int) bool { return devices[i].No < devices[j].No })
	for i := range devices {
		// The arrival time names the device (« arrivé à 21 h 05 »); an
		// event from before it carried one is named at the window's opening.
		if devices[i].AsOf.IsZero() {
			devices[i].AsOf = ev.OpenedAt
		}
		if devices[i].AsOf.IsZero() {
			devices[i].AsOf = now
		}
	}
	alert := domain.Alert{
		TenantID: ev.TenantID, WindowID: ev.WindowID, Devices: devices,
		CreatedAt: now, ReplyUntil: now.Add(domain.ReplyWindow),
	}
	created, err := uc.Repo.CreateAlert(ctx, alert)
	if err != nil {
		return err
	}
	if !created {
		// The outbox delivers at least once; the household is told once.
		return nil
	}
	stored, err := uc.Repo.AlertByWindow(ctx, ev.TenantID, ev.WindowID)
	if err != nil {
		return err
	}
	return uc.send(ctx, stored)
}

// send delivers one alert over both channels.
func (uc Alerts) send(ctx context.Context, alert domain.Alert) error {
	optIns, err := uc.Repo.ActiveOptIns(ctx, alert.TenantID)
	if err != nil {
		return err
	}
	text := ComposeAlert(alert)
	for _, o := range optIns {
		id, sendErr := uc.Notifier.Send(ctx, o.Phone, domain.OutboundMessage{Text: text, AlertID: alert.ID})
		delivery := domain.Delivery{
			AlertID: alert.ID, TenantID: alert.TenantID, UserID: o.UserID,
			Channel: "whatsapp", Provider: uc.Notifier.Name(),
			ProviderMsgID: id, SentAt: uc.Now().UTC(),
		}
		if sendErr != nil {
			// The failure is recorded, not hidden: the app shows "the
			// message could not be sent" instead of leaving the household
			// to wonder, and an activation problem is an administrator's to
			// fix, so it has to be visible.
			delivery.Err = sendErr.Error()
			if uc.Log != nil {
				uc.Log.Warn("alerts: could not send an alert",
					"provider", uc.Notifier.Name(), "to", o.Phone.Masked(),
					"tenant_id", alert.TenantID,
					"activation_required", errors.Is(sendErr, domain.ErrNotActivated),
					"error", sendErr)
			}
		}
		if err := uc.Repo.RecordDelivery(ctx, delivery); err != nil {
			return err
		}
	}
	if len(optIns) == 0 && uc.Log != nil {
		uc.Log.Info("alerts: nobody has opted in to WhatsApp for this household",
			"tenant_id", alert.TenantID, "devices", len(alert.Devices))
	}
	return uc.push(ctx, alert)
}

// push is the second channel (issue #49): every installation of the
// household that registered a token, whether or not its member linked
// WhatsApp — the app is the channel of whoever did not. Each is recorded
// like a WhatsApp message, and a token the provider says is gone is
// forgotten. A push that fails is not a reason to retry the window: the
// WhatsApp messages are already out, and a retry would send them twice.
func (uc Alerts) push(ctx context.Context, alert domain.Alert) error {
	if uc.Pusher == nil || uc.Targets == nil || len(alert.Devices) == 0 {
		return nil
	}
	targets, err := uc.Targets.For(ctx, alert.TenantID)
	if err != nil {
		if uc.Log != nil {
			uc.Log.Warn("alerts: cannot list push targets", "tenant_id", alert.TenantID, "error", err)
		}
		return nil
	}
	msg := domain.PushMessage{
		Title: PushTitle(alert),
		Body:  ComposePush(alert),
		// The first device's MAC is what the app opens: its sheet, where it
		// can be blocked. The others are in the device list right behind.
		Data: map[string]string{
			"kind": "new_device", "tenantId": alert.TenantID, "alertId": alert.ID,
			"mac": alert.Devices[0].MAC,
		},
		// The app's « Approuver » button (issue #58), next to opening the
		// sheet where it can be blocked.
		Category: PushCategoryApprove,
	}
	for _, t := range targets {
		pushErr := uc.Pusher.Push(ctx, t, msg)
		delivery := domain.Delivery{
			AlertID: alert.ID, TenantID: alert.TenantID, UserID: t.UserID,
			Channel: "push", Provider: uc.Pusher.Name(), SentAt: uc.Now().UTC(),
		}
		if pushErr != nil {
			delivery.Err = pushErr.Error()
			if errors.Is(pushErr, domain.ErrPushTokenGone) {
				if err := uc.Targets.Forget(ctx, t.Token); err != nil && uc.Log != nil {
					uc.Log.Warn("alerts: cannot forget a gone push token", "error", err)
				}
			} else if uc.Log != nil {
				uc.Log.Warn("alerts: push failed", "provider", uc.Pusher.Name(),
					"user_id", t.UserID, "platform", t.Platform, "error", pushErr)
			}
		}
		if err := uc.Repo.RecordDelivery(ctx, delivery); err != nil {
			return err
		}
	}
	return nil
}

// PushCategoryApprove is the app's notification category that carries
// « Autoriser » (mobile/src/alerts/alerts.ts, APPROVE_CATEGORY).
const PushCategoryApprove = "new-device-approve"

// ComposeApproved answers « OK » (or the « 2 » of a one-device alert,
// still understood but no longer taught): the device will not be signalled
// again.
func ComposeApproved(d domain.AlertDevice) string {
	n := nameOf(d)
	return "👍 " + n.definite() + " est autorisé" + n.e() + " : Kergui ne vous " + n.pronoun() + " signalera plus."
}

// ComposeApproveFailed says an « OK » could not be recorded.
func ComposeApproveFailed(d domain.AlertDevice) string {
	n := nameOf(d)
	return "❌ " + n.definite() + " n'a pas pu être autorisé" + n.e() + ". Ouvrez l'application Kergui pour réessayer."
}

// PushTitle is the notification's title.
func PushTitle(alert domain.Alert) string {
	if len(alert.Devices) == 1 {
		return "Appareil à vérifier sur votre Wi-Fi"
	}
	return fmt.Sprintf("%d appareils à vérifier sur votre Wi-Fi", len(alert.Devices))
}

// ComposeAlert writes the WhatsApp message.
//
// It is French, short, and says what to answer. The audience is
// non-technical (RFC-005 C3), so each device is named by what it probably
// is and when it arrived (DeviceLabel), never by an address; the MAC stays
// in the alert for the audit trail.
//
// One answer has one meaning, whatever the alert (audit C2): a number is the
// device to block, « OK » authorises. A person who learned « OK » on an
// alert about one device does not block device n° 2 on the next one.
func ComposeAlert(alert domain.Alert) string {
	var b strings.Builder
	if len(alert.Devices) == 1 {
		b.WriteString("Kergui : un appareil à vérifier vient de rejoindre votre Wi-Fi.\n\n")
	} else {
		fmt.Fprintf(&b, "Kergui : %d appareils à vérifier viennent de rejoindre votre Wi-Fi.\n\n", len(alert.Devices))
	}
	for _, d := range alert.Devices {
		fmt.Fprintf(&b, "%d. %s\n", d.No, DeviceLabel(d))
	}
	b.WriteString("\n")
	if len(alert.Devices) == 1 {
		fmt.Fprintf(&b, "Répondez %d pour le bloquer, ou OK pour l'autoriser.", alert.Devices[0].No)
	} else {
		b.WriteString("Répondez le numéro de l'appareil à bloquer (1, 2…), ou OK pour tous les autoriser.")
	}
	return b.String()
}

// ComposePush is the same news in one line, for a notification. The
// notification's one button authorises; blocking is done on the device's
// sheet, which the notification opens, and the text says so.
func ComposePush(alert domain.Alert) string {
	if len(alert.Devices) == 1 {
		return DeviceLabel(alert.Devices[0]) + ", vient de rejoindre votre Wi-Fi. Ouvrez pour l'autoriser ou le bloquer."
	}
	names := make([]string, 0, len(alert.Devices))
	for _, d := range alert.Devices {
		n := nameOf(d)
		n.at = "" // one time for all is the notification's own
		names = append(names, lowerFirst(n.indefinite()))
	}
	return fmt.Sprintf("%d appareils viennent de rejoindre votre Wi-Fi : %s. Ouvrez pour les autoriser ou les bloquer.",
		len(alert.Devices), strings.Join(names, ", "))
}

// ComposeAmbiguous answers a reply that quotes no alert while several are
// open: acting on the newest could block a device of another alert than the
// one the person is reading (audit C2).
const ComposeAmbiguous = "Pour quelle alerte ? Répondez directement au message de l'alerte (appui long, puis Répondre)."

// ComposeOptInConfirmation is the message that answers "KERGUI <code>".
// The provider's licence notice goes here: it is the one message the person
// receives before any alert, and the owner's settings screen repeats it.
func ComposeOptInConfirmation(notice string) string {
	b := strings.Builder{}
	b.WriteString("C'est fait : vous recevrez une alerte ici dès qu'un appareil à vérifier rejoint votre Wi-Fi, " +
		"et vous pourrez le bloquer en répondant à ce message.\n\n" +
		"Envoyez STOP à tout moment pour ne plus recevoir ces alertes.")
	if notice != "" {
		b.WriteString("\n\n" + notice)
	}
	return b.String()
}

// ComposeBlocked, ComposeAlreadyBlocked, ComposeBlockPending and
// ComposeRefused answer a reply. They land with the "1 = block" path, and
// live here so every household-facing sentence of this module is in one
// place.
func ComposeBlocked(d domain.AlertDevice) string {
	n := nameOf(d)
	return "✅ " + n.definite() + " est bloqué" + n.e() + "."
}

// ComposeAlreadyBlocked answers the same reply twice.
func ComposeAlreadyBlocked(d domain.AlertDevice) string {
	n := nameOf(d)
	return "✅ " + n.definite() + " était déjà bloqué" + n.e() + "."
}

// ComposeBlockPending answers a block the relay has not confirmed yet.
func ComposeBlockPending(d domain.AlertDevice) string {
	return "⏳ " + nameOf(d).definite() + " : demande envoyée, en attente de la box. " +
		"Vous verrez le résultat dans l'application Kergui."
}

// ComposeRefused says why nothing happened, in words that mean something to
// the household rather than the hub's error code.
func ComposeRefused(d domain.AlertDevice, reason string) string {
	n := nameOf(d)
	return "❌ " + n.definite() + " n'a pas été bloqué" + n.e() + " : " + reason
}

func lowerFirst(s string) string {
	for i, r := range s {
		return strings.ToLower(string(r)) + s[i+len(string(r)):]
	}
	return s
}

// FormatChoices renders a list of answered numbers, for a log line or an
// audit entry.
func FormatChoices(choices []int) string {
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, strconv.Itoa(c))
	}
	return strings.Join(out, ",")
}
