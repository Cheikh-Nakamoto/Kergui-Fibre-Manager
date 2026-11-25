package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	licdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// The renewal reminders' switch (issue #99), on the « Passes et paiements »
// page:
//
//	GET /api/v1/admin/settings/renewal-reminders
//	PUT /api/v1/admin/settings/renewal-reminders {enabled}
//
// It is one key of hub_settings, the table of the hub's settings the
// back-office edits (the complaints' admin numbers are another). With no
// row the reminders are on: they were, before there was a switch. Switched
// off, nothing is sent and nothing is lost: the reminders still due when it
// is back on go out then.

// SettingReminders is the hub_settings key: {"enabled": bool}.
const SettingReminders = "renewal_reminders"

// remindersSetting is the switch, with the hours the reminders go out.
type remindersSetting struct {
	Enabled bool `json:"enabled"`
	// FromHour and ToHour are the hours of the day, in Dakar, reminders go
	// out between; the back-office shows them.
	FromHour  int        `json:"fromHour"`
	ToHour    int        `json:"toHour"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

func (m *Module) remindersSetting(ctx context.Context) (remindersSetting, error) {
	out := remindersSetting{Enabled: true, FromHour: licdomain.ReminderFromHour, ToHour: licdomain.ReminderToHour}
	var (
		raw []byte
		at  time.Time
	)
	err := m.cfg.Pool.QueryRow(ctx, `SELECT value, updated_at FROM hub_settings WHERE key = $1`, SettingReminders).Scan(&raw, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var v struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return out, err
	}
	if v.Enabled != nil {
		out.Enabled = *v.Enabled
	}
	at = at.UTC()
	out.UpdatedAt = &at
	return out, nil
}

// RemindersOn reports whether the renewal reminders are on: the licensing
// module's sweep asks before sending any.
func (m *Module) RemindersOn(ctx context.Context) (bool, error) {
	s, err := m.remindersSetting(ctx)
	return s.Enabled, err
}

func (m *Module) getReminders(w http.ResponseWriter, r *http.Request) {
	s, err := m.remindersSetting(r.Context())
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, s)
}

func (m *Module) putReminders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		response.Error(w, kernelerrors.BadRequest("enabled: true or false"))
		return
	}
	before, err := m.remindersSetting(ctx)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	value, _ := json.Marshal(map[string]bool{"enabled": *req.Enabled})
	var by *string
	if id := kernelauth.UserIDFromContext(ctx); id != "" {
		by = &id
	}
	if _, err := m.cfg.Pool.Exec(ctx, `
		INSERT INTO hub_settings (key, value, updated_at, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		SettingReminders, value, m.cfg.Now().UTC(), by); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	state := map[bool]string{true: "activés", false: "désactivés"}[*req.Enabled]
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindRemindersSet, TargetType: actionlog.TargetSettings,
		Summary: fmt.Sprintf("Rappels de renouvellement %s", state),
		Details: map[string]any{"setting": SettingReminders, "enabled": *req.Enabled, "before": before.Enabled},
	})
	s, err := m.remindersSetting(ctx)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, s)
}

// reminderSent is one renewal reminder of a pass's end date, as the
// expiring list's « rappels envoyés » column shows it.
type reminderSent struct {
	// Step is j3 (three days before) or j0 (the day it ends).
	Step   string    `json:"step"`
	SentAt time.Time `json:"sentAt"`
	// WhatsApp is sending, sent, failed or skipped; WhatsAppTries how many
	// times it was tried.
	WhatsApp      string `json:"whatsapp"`
	WhatsAppTries int    `json:"whatsappTries"`
	// Push is the app notification: sending, sent, failed, none (no phone
	// with the app), off (no push on this hub) or skipped.
	Push       string `json:"push"`
	PushPhones int    `json:"pushPhones"`
	PushSent   int    `json:"pushSent"`
	// Note says why a reminder was skipped.
	Note string `json:"note,omitempty"`
}

// remindersOf selects, in SQL, the reminders of the pass s's current end
// date as a JSON array, oldest first.
const remindersOf = `COALESCE((SELECT json_agg(json_build_object(
	  'step', r.step, 'sentAt', r.sent_at, 'whatsapp', r.whatsapp, 'whatsappTries', r.whatsapp_tries,
	  'push', r.push, 'pushPhones', r.push_phones, 'pushSent', r.push_sent, 'note', NULLIF(r.note, '')) ORDER BY r.sent_at)
	FROM hub_licensing_reminders r
	WHERE r.account_id = s.account_id AND r.box_id = s.box_id AND r.expires_at = s.expires_at), '[]')`
