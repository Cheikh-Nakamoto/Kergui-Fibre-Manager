package messages

// The WhatsApp channel (issue #81): who a WhatsApp message may reach, and
// the queue that sends it one number at a time.
//
// whatsmeow is not WhatsApp's official API and bulk sending gets the Kergui
// number banned (owner's decision, 2026-11-23). So:
//
//   - the audience is one household (its opted-in numbers) or a
//     hand-picked group of at most GroupMax numbers;
//   - only numbers with a running opt-in (hub_whatsapp_optins, stopped_at
//     null) receive it; the others are refused and listed;
//   - the send is queued in Postgres (hub_whatsapp_queue) and one worker
//     sends one message every SendInterval (40 s), the pace being a row
//     shared by every replica (hub_whatsapp_pace).
//
// The alerts never enter this queue: they keep their direct path through
// the notifier, so an urgent alert never waits behind an announcement.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Defaults of the WhatsApp channel.
const (
	DefaultSendInterval = 40 * time.Second
	DefaultGroupMax     = 20
	DefaultRetryDelay   = 2 * time.Minute
	// MaxAttempts is how many times one number is tried before its
	// delivery is recorded as failed.
	MaxAttempts = 3
	// sendTimeout bounds one send, so a stuck provider does not hold the
	// queue (and its row lock) forever.
	sendTimeout = 30 * time.Second
)

// Why a number is refused.
const (
	RefusedInvalid    = "invalid"      // not a phone number
	RefusedNotOptedIn = "not_opted_in" // never activated WhatsApp with Kergui
	RefusedStopped    = "stopped"      // sent STOP
)

type waRecipient struct {
	Phone    alertsdomain.Phone
	TenantID string
	UserID   string
}

type waRefused struct {
	Number string `json:"number"`
	Reason string `json:"reason"`
}

// waAudience is who a WhatsApp message reaches.
type waAudience struct {
	Kind, Value string
	Household   *householdRef
	Recipients  []waRecipient
	Refused     []waRefused
}

// whatsAppPreview is what the preview adds for WhatsApp.
type whatsAppPreview struct {
	Recipients      []string    `json:"recipients"`
	Refused         []waRefused `json:"refused"`
	IntervalSeconds float64     `json:"intervalSeconds"`
	// Ahead is what the queue has to send before this message; Estimated
	// is how long, in seconds, until its last number is sent.
	Ahead            int     `json:"ahead"`
	EstimatedSeconds float64 `json:"estimatedSeconds"`
	GroupMax         int     `json:"groupMax"`
}

var errTooMany = errors.New("too many numbers")

// resolveWhatsApp reads a WhatsApp audience: a household or numbers.
func (m *Module) resolveWhatsApp(ctx context.Context, kind, value string, numbers []string) (waAudience, error) {
	a := waAudience{Kind: strings.TrimSpace(kind)}
	switch a.Kind {
	case AudienceHousehold:
		h, err := m.findHousehold(ctx, strings.TrimSpace(value))
		if err != nil {
			return a, err
		}
		a.Value, a.Household = h.TenantID, &h
		rows, err := m.cfg.Pool.Query(ctx, `
			SELECT phone, user_id::text, stopped_at IS NOT NULL
			FROM hub_whatsapp_optins WHERE tenant_id = $1
			ORDER BY stopped_at IS NOT NULL, created_at`, h.TenantID)
		if err != nil {
			return a, err
		}
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var phone, user string
			var stopped bool
			if err := rows.Scan(&phone, &user, &stopped); err != nil {
				return a, err
			}
			if seen[phone] {
				continue
			}
			seen[phone] = true
			if stopped {
				a.Refused = append(a.Refused, waRefused{Number: phone, Reason: RefusedStopped})
				continue
			}
			a.Recipients = append(a.Recipients, waRecipient{Phone: alertsdomain.Phone(phone), TenantID: h.TenantID, UserID: user})
		}
		return a, rows.Err()
	case AudienceNumbers:
		return a, m.resolveNumbers(ctx, &a, numbers)
	default:
		return a, fmt.Errorf("%w: audience: household or numbers for WhatsApp", errBadAudience)
	}
}

// resolveNumbers checks each hand-picked number against the opt-ins.
func (m *Module) resolveNumbers(ctx context.Context, a *waAudience, numbers []string) error {
	var phones []string
	seen := map[string]bool{}
	entries := 0
	for _, raw := range numbers {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := alertsdomain.ParsePhone(raw)
		key := raw
		if err == nil {
			key = string(p)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		entries++
		if err != nil {
			a.Refused = append(a.Refused, waRefused{Number: raw, Reason: RefusedInvalid})
			continue
		}
		phones = append(phones, string(p))
	}
	if entries == 0 {
		return fmt.Errorf("%w: numbers: at least one number", errBadAudience)
	}
	if entries > m.cfg.GroupMax {
		return fmt.Errorf("%w: numbers: at most %d at once", errTooMany, m.cfg.GroupMax)
	}
	type optin struct {
		tenant, user string
		stopped      bool
	}
	found := map[string]optin{}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT phone, tenant_id::text, user_id::text, stopped_at IS NOT NULL
		FROM hub_whatsapp_optins WHERE phone = ANY($1)
		ORDER BY stopped_at IS NOT NULL, created_at`, phones)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var phone string
		var o optin
		if err := rows.Scan(&phone, &o.tenant, &o.user, &o.stopped); err != nil {
			return err
		}
		// A running opt-in comes first; a number in two households is
		// sent to once, through its first one.
		if _, ok := found[phone]; !ok {
			found[phone] = o
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range phones {
		o, ok := found[p]
		switch {
		case !ok:
			a.Refused = append(a.Refused, waRefused{Number: p, Reason: RefusedNotOptedIn})
		case o.stopped:
			a.Refused = append(a.Refused, waRefused{Number: p, Reason: RefusedStopped})
		default:
			a.Recipients = append(a.Recipients, waRecipient{Phone: alertsdomain.Phone(p), TenantID: o.tenant, UserID: o.user})
		}
	}
	return nil
}

func (a waAudience) households() int {
	seen := map[string]bool{}
	for _, r := range a.Recipients {
		seen[r.TenantID] = true
	}
	return len(seen)
}

// previewWhatsApp says who receives it, who is refused, and how long the
// queue takes.
func (m *Module) previewWhatsApp(ctx context.Context, a waAudience) (previewResponse, error) {
	var ahead int
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT count(*) FROM hub_whatsapp_queue WHERE status = 'queued'`).Scan(&ahead); err != nil {
		return previewResponse{}, err
	}
	return m.whatsAppResponse(a, ahead), nil
}

func (m *Module) whatsAppResponse(a waAudience, ahead int) previewResponse {
	n := len(a.Recipients)
	recipients := make([]string, 0, n)
	for _, r := range a.Recipients {
		recipients = append(recipients, string(r.Phone))
	}
	refused := a.Refused
	if refused == nil {
		refused = []waRefused{}
	}
	var estimated float64
	if n > 0 {
		estimated = float64(ahead+n-1) * m.cfg.SendInterval.Seconds()
	}
	hh := a.households()
	return previewResponse{
		Channel: ChannelWhatsApp, Audience: a.Kind, Value: a.Value, Household: a.Household,
		Households: hh, HouseholdsWithApp: hh, Phones: n,
		whatsAppPreview: &whatsAppPreview{
			Recipients: recipients, Refused: refused, IntervalSeconds: m.cfg.SendInterval.Seconds(),
			Ahead: ahead, EstimatedSeconds: estimated, GroupMax: m.cfg.GroupMax,
		},
	}
}

// sendWhatsApp queues a message for its opted-in numbers.
func (m *Module) sendWhatsApp(w http.ResponseWriter, r *http.Request, title, body string, req sendRequest) {
	ctx := r.Context()
	a, err := m.resolveWhatsApp(ctx, req.Audience, req.Value, req.Numbers)
	if err != nil {
		audienceError(w, err)
		return
	}
	if m.cfg.Notifier == nil {
		response.Error(w, kernelerrors.ServiceUnavailable("WhatsApp is not configured on this hub"))
		return
	}
	if len(a.Recipients) == 0 {
		refused := a.Refused
		if refused == nil {
			refused = []waRefused{}
		}
		response.JSON(w, http.StatusConflict, map[string]any{
			"code": "CONFLICT", "message": "no opted-in WhatsApp number in this audience", "refused": refused,
		})
		return
	}

	id := uuid.NewString()
	var tenant *string
	if a.Household != nil {
		tenant = &a.Household.TenantID
	}
	now := m.cfg.Now().UTC()
	tx, err := m.cfg.Pool.Begin(ctx)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var ahead int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM hub_whatsapp_queue WHERE status = 'queued'`).Scan(&ahead); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO hub_messages (id, title, body, channel, audience, audience_value, tenant_id, author_id, households, phones, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, title, body, ChannelWhatsApp, a.Kind, a.Value, tenant, kernelauth.UserIDFromContext(ctx),
		a.households(), len(a.Recipients), now); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	for _, rc := range a.Recipients {
		var user *string
		if rc.UserID != "" {
			user = &rc.UserID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO hub_whatsapp_queue (message_id, tenant_id, user_id, phone, next_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5, $5)`, id, rc.TenantID, user, string(rc.Phone), now); err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}

	out := sendResponse{MessageID: id, previewResponse: m.whatsAppResponse(a, ahead), Queued: len(a.Recipients)}
	label := "foyer " + a.Value
	if a.Household != nil && a.Household.Phone != "" {
		label = "foyer " + a.Household.Phone
	} else if a.Kind == AudienceNumbers {
		label = fmt.Sprintf("%d numéro(s) choisis", len(a.Recipients)+len(a.Refused))
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindAnnouncementSent, TenantID: deref(tenant),
		TargetType: actionlog.TargetMessage, TargetID: id,
		Summary: fmt.Sprintf("Message WhatsApp « %s » mis en file (%s) : %d numéro(s), %d refusé(s)",
			title, label, len(a.Recipients), len(a.Refused)),
		Details: map[string]any{"title": title, "channel": ChannelWhatsApp, "audience": a.Kind, "value": a.Value,
			"households": a.households(), "phones": len(a.Recipients), "refused": len(a.Refused),
			"intervalSeconds": m.cfg.SendInterval.Seconds()},
	})
	response.JSON(w, http.StatusCreated, out)
}

// whatsAppText is what the number receives.
func whatsAppText(title, body string) string {
	return "*" + title + "*\n\n" + body + "\n\n— Kergui · Répondez STOP pour ne plus rien recevoir de Kergui sur WhatsApp."
}

// --- the queue --------------------------------------------------------------------

// Run sends the WhatsApp queue until ctx ends: one message, then the
// interval, then the next. Rows left queued by a hub that stopped are sent
// by the next one; rows another replica holds are skipped.
func (m *Module) Run(ctx context.Context) error {
	if m.cfg.Notifier == nil {
		<-ctx.Done()
		return nil
	}
	poll := min(m.cfg.SendInterval/4, time.Second)
	if poll <= 0 {
		poll = time.Millisecond
	}
	for {
		wait, err := m.SendNext(ctx)
		if err != nil && ctx.Err() == nil && m.cfg.Logger != nil {
			m.cfg.Logger.Warn("messages: the WhatsApp queue", "error", err)
		}
		if wait <= 0 {
			wait = poll
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-m.wake:
			timer.Stop()
			if wait > poll {
				// The pace still holds: wait for it, not for the wake.
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(wait):
				}
			}
		case <-timer.C:
		}
	}
}

// SendNext sends at most one queued message, if the pace allows it, and
// says how long to wait before trying again (0: the usual poll).
func (m *Module) SendNext(ctx context.Context) (time.Duration, error) {
	// The bookkeeping must land even when ctx ends during the send.
	db := context.WithoutCancel(ctx)
	tx, err := m.cfg.Pool.Begin(db)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(db) }()

	// The pace: whoever holds this row is the one sender.
	var last *time.Time
	if err := tx.QueryRow(db, `SELECT last_sent_at FROM hub_whatsapp_pace WHERE id = 1 FOR UPDATE SKIP LOCKED`).Scan(&last); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil // another replica is sending
		}
		return 0, err
	}
	now := m.cfg.Now().UTC()
	if last != nil {
		if next := last.Add(m.cfg.SendInterval); now.Before(next) {
			return next.Sub(now), nil
		}
	}

	var (
		rowID                    int64
		messageID, tenant, phone string
		user                     *string
		attempts                 int
		title, body              string
		optedIn                  bool
	)
	err = tx.QueryRow(db, `
		SELECT q.id, q.message_id::text, q.tenant_id::text, q.user_id::text, q.phone, q.attempts, m.title, m.body,
		       EXISTS (SELECT 1 FROM hub_whatsapp_optins o WHERE o.phone = q.phone AND o.stopped_at IS NULL)
		FROM hub_whatsapp_queue q JOIN hub_messages m ON m.id = q.message_id
		WHERE q.status = 'queued' AND q.next_at <= $1
		ORDER BY q.next_at, q.id LIMIT 1
		FOR UPDATE OF q SKIP LOCKED`, now).
		Scan(&rowID, &messageID, &tenant, &user, &phone, &attempts, &title, &body, &optedIn)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	provider := m.cfg.Notifier.Name()
	if !optedIn {
		// STOP since it was queued: not sent, and no pace spent.
		if err := m.settle(db, tx, rowID, messageID, tenant, user, phone, provider, "failed", attempts, now, "", "opted out (STOP) before it was sent"); err != nil {
			return 0, err
		}
		return 0, tx.Commit(db)
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	providerID, sendErr := m.cfg.Notifier.Send(sendCtx, alertsdomain.Phone(phone), alertsdomain.OutboundMessage{Text: whatsAppText(title, body)})
	cancel()
	if sendErr != nil && ctx.Err() != nil {
		// The hub is stopping: the row stays queued for the next start.
		return 0, nil
	}
	after := m.cfg.Now().UTC()
	attempts++
	switch {
	case sendErr == nil:
		err = m.settle(db, tx, rowID, messageID, tenant, user, phone, provider, "sent", attempts, after, providerID, "")
	case attempts >= MaxAttempts:
		err = m.settle(db, tx, rowID, messageID, tenant, user, phone, provider, "failed", attempts, after, "", sendErr.Error())
	default:
		_, err = tx.Exec(db, `
			UPDATE hub_whatsapp_queue SET attempts = $2, next_at = $3, last_error = $4, updated_at = $5 WHERE id = $1`,
			rowID, attempts, after.Add(time.Duration(attempts)*m.cfg.RetryDelay), sendErr.Error(), after)
	}
	if err != nil {
		return 0, err
	}
	if sendErr != nil && m.cfg.Logger != nil {
		m.cfg.Logger.Warn("messages: WhatsApp send failed", "provider", provider,
			"to", alertsdomain.Phone(phone).Masked(), "attempt", attempts, "error", sendErr)
	}
	if _, err := tx.Exec(db, `UPDATE hub_whatsapp_pace SET last_sent_at = $1 WHERE id = 1`, after); err != nil {
		return 0, err
	}
	if err := tx.Commit(db); err != nil {
		return 0, err
	}
	return m.cfg.SendInterval, nil
}

// settle closes a queue row and records its delivery.
func (m *Module) settle(ctx context.Context, tx pgx.Tx, rowID int64, messageID, tenant string, user *string,
	phone, provider, status string, attempts int, at time.Time, providerID, reason string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE hub_whatsapp_queue SET status = $2, attempts = $3, provider_msg_id = $4, last_error = $5, updated_at = $6
		WHERE id = $1`, rowID, status, attempts, providerID, reason, at); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO hub_message_deliveries (message_id, tenant_id, user_id, channel, provider, device, status, error, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		messageID, tenant, user, ChannelWhatsApp, provider, alertsdomain.Phone(phone).Masked(), status, reason, at)
	return err
}
