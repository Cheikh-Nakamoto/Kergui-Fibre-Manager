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
		uc.push(ctx, alert, o)
	}
	if len(optIns) == 0 && uc.Log != nil {
		uc.Log.Info("alerts: nobody has opted in for this household",
			"tenant_id", alert.TenantID, "devices", len(alert.Devices))
	}
	return nil
}

// push is the second channel. In H2 there are no device tokens yet, so it
// walks the port and finds nobody; the path is in place for H3.
func (uc Alerts) push(ctx context.Context, alert domain.Alert, o domain.OptIn) {
	if uc.Pusher == nil || uc.Targets == nil {
		return
	}
	targets, err := uc.Targets.For(ctx, alert.TenantID, o.UserID)
	if err != nil {
		if uc.Log != nil {
			uc.Log.Warn("alerts: cannot list push targets", "tenant_id", alert.TenantID, "error", err)
		}
		return
	}
	msg := domain.PushMessage{
		Title: "Nouvel appareil sur votre Wi-Fi",
		Body:  ComposePush(alert),
		Data:  map[string]string{"tenantId": alert.TenantID, "alertId": alert.ID},
	}
	for _, t := range targets {
		if err := uc.Pusher.Push(ctx, t, msg); err != nil && uc.Log != nil {
			uc.Log.Warn("alerts: push failed", "user_id", t.UserID, "error", err)
		}
	}
}

// ComposeAlert writes the WhatsApp message.
//
// It is French, short, and says what to answer. The audience is
// non-technical (RFC-005 C3): a MAC address means nothing to them, so the
// message shows the name the box reported and the address on the network,
// and keeps the MAC for the audit trail.
func ComposeAlert(alert domain.Alert) string {
	var b strings.Builder
	if len(alert.Devices) == 1 {
		b.WriteString("Kergui : un nouvel appareil vient de rejoindre votre Wi-Fi.\n\n")
	} else {
		fmt.Fprintf(&b, "Kergui : %d nouveaux appareils viennent de rejoindre votre Wi-Fi.\n\n", len(alert.Devices))
	}
	for _, d := range alert.Devices {
		fmt.Fprintf(&b, "%d. %s\n", d.No, describe(d))
	}
	b.WriteString("\n")
	if len(alert.Devices) == 1 {
		b.WriteString("Répondez 1 pour bloquer cet appareil.")
	} else {
		b.WriteString("Répondez par le numéro de l'appareil à bloquer (par exemple « 1 », ou « 1 2 »).")
	}
	return b.String()
}

// ComposePush is the same news in one line, for a notification.
func ComposePush(alert domain.Alert) string {
	if len(alert.Devices) == 1 {
		return describe(alert.Devices[0]) + " vient de rejoindre votre Wi-Fi."
	}
	names := make([]string, 0, len(alert.Devices))
	for _, d := range alert.Devices {
		names = append(names, describe(d))
	}
	return strings.Join(names, ", ") + " viennent de rejoindre votre Wi-Fi."
}

// ComposeOptInConfirmation is the message that answers "KERGUI <code>".
// The provider's licence notice goes here: it is the one message the person
// receives before any alert, and the owner's settings screen repeats it.
func ComposeOptInConfirmation(notice string) string {
	b := strings.Builder{}
	b.WriteString("C'est fait : vous recevrez une alerte ici dès qu'un appareil inconnu rejoint votre Wi-Fi, " +
		"et vous pourrez le bloquer en répondant à ce message.\n\n" +
		"Envoyez STOP à tout moment pour ne plus recevoir ces alertes.")
	if notice != "" {
		b.WriteString("\n\n" + notice)
	}
	return b.String()
}

// ComposeBlocked and ComposeRefused answer a reply. They land with the
// "1 = block" path, and live here so every household-facing sentence of
// this module is in one place.
func ComposeBlocked(d domain.AlertDevice) string {
	return "✅ " + describe(d) + " est bloqué."
}

// ComposeRefused says why nothing happened, in words that mean something to
// the household rather than the hub's error code.
func ComposeRefused(d domain.AlertDevice, reason string) string {
	return "❌ " + describe(d) + " n'a pas été bloqué : " + reason
}

// describe is how a device is named to a household: the name the box
// reported, else its address on the network, else — as a last resort — its
// MAC, because something has to identify it.
func describe(d domain.AlertDevice) string {
	switch {
	case d.Hostname != "" && d.IP != "":
		return d.Hostname + " (" + d.IP + ")"
	case d.Hostname != "":
		return d.Hostname
	case d.IP != "":
		return "appareil inconnu (" + d.IP + ")"
	default:
		return "appareil " + d.MAC
	}
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
