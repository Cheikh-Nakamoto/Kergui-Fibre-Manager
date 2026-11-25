package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	licdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/messages"
)

// The pass gestures of a household's page (issue #98): renew or extend a
// box's pass by hand, give a lifetime pass, take it back.
//
//	POST /api/v1/admin/households/{tenantId}/passes/renew           {boxId, tier, days?, reason, amountXof?}
//	POST /api/v1/admin/households/{tenantId}/passes/lifetime        {boxId, tier, reason}
//	POST /api/v1/admin/households/{tenantId}/passes/lifetime/revoke {boxId, reason}
//
// The pass is the one of the account that owns the household (its phone
// number). How they relate to the gestures already there:
//
//   - « Geste commercial » (POST passes/grant) gives a catalogue pass at 0 F;
//     « Prolonger » on the support page (support/.../extend) adds days on the
//     tier the box has, for an outage, with no payment.
//   - « Renouveler » here is the general form of both: any tier, any number
//     of days (30 by default), from the current end while a pass runs (the
//     purchase rule, Basic time converted when Max is chosen), and the money
//     received by hand if any. Paid, it is a "manual" payment that counts in
//     the revenue like a NabooPay one; given, a "support" one at 0 F like the
//     gesture. Either way the renewal ledger (#97) records it through its
//     trigger, as for every other write path.
//
// Each gesture lands in the action log with its reason, and the household
// is told by push, filed in the app's notifications (Notifier).

// Notifier tells one household something by push, as an announcement the
// app files in its notifications: the messages module's NotifyHousehold.
type Notifier func(ctx context.Context, tenantID, title, body string) (messages.Notified, error)

// defaultRenewDays is a month, as the catalogue counts one.
const defaultRenewDays = 30

// maxManualAmount bounds the amount typed by hand: ten years of Max.
const maxManualAmount = 200_000

// householdAccount is the account that owns a household, answering 400,
// 404 or 409 itself.
func (m *Module) householdAccount(w http.ResponseWriter, r *http.Request) (tenant string, acc licusecase.Account, ok bool) {
	if !m.supportOn(w) {
		return "", acc, false
	}
	tenant = r.PathValue("tenantId")
	if _, err := uuid.Parse(tenant); err != nil {
		response.Error(w, kernelerrors.BadRequest("not a household id"))
		return "", acc, false
	}
	var account *string
	err := m.cfg.Pool.QueryRow(r.Context(), `SELECT h.account_id::text FROM tenants t
		LEFT JOIN hub_identity_households h ON h.tenant_id = t.id WHERE t.id = $1`, tenant).Scan(&account)
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, kernelerrors.NotFound("no such household"))
		return "", acc, false
	}
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return "", acc, false
	}
	if account == nil {
		// Signed up by e-mail: no phone number, so no pass to hold.
		response.Error(w, kernelerrors.Conflict("this household has no phone account to hold a pass"))
		return "", acc, false
	}
	acc, err = m.cfg.Support.AccountByID(r.Context(), licdomain.AccountID(*account))
	if err != nil {
		supportError(w, err, "no such account")
		return "", acc, false
	}
	return tenant, acc, true
}

// current is the account's pass on box before a gesture, zero if none.
func (m *Module) current(ctx context.Context, a licdomain.AccountID, box licdomain.BoxID) licdomain.Subscription {
	subs, err := m.cfg.Support.Subscriptions(ctx, a)
	if err == nil {
		for _, s := range subs {
			if s.BoxID == box {
				return s
			}
		}
	}
	return licdomain.Subscription{}
}

// notify tells the household, without failing the gesture: the pass is
// changed whatever the push provider says, and the answer reports it.
func (m *Module) notify(ctx context.Context, tenant, title, body string) map[string]any {
	if m.cfg.Notify == nil {
		return map[string]any{"phones": 0, "sent": 0, "failed": 0, "off": true}
	}
	n, err := m.cfg.Notify(ctx, tenant, title, body)
	if errors.Is(err, messages.ErrPushOff) {
		return map[string]any{"phones": 0, "sent": 0, "failed": 0, "off": true}
	}
	if err != nil {
		return map[string]any{"phones": n.Phones, "sent": n.Sent, "failed": n.Failed, "error": err.Error()}
	}
	return map[string]any{"phones": n.Phones, "sent": n.Sent, "failed": n.Failed, "messageId": n.MessageID}
}

func parseTier(raw string) (licdomain.Tier, bool) {
	switch t := licdomain.Tier(strings.ToLower(strings.TrimSpace(raw))); t {
	case licdomain.TierBasic, licdomain.TierMax:
		return t, true
	}
	return "", false
}

func parseBox(w http.ResponseWriter, raw string) (licdomain.BoxID, bool) {
	box, err := licdomain.ParseBoxID(raw)
	if err != nil {
		response.Error(w, kernelerrors.BadRequest("boxId: not a box serial number"))
		return "", false
	}
	return box, true
}

// passError maps the refusals of the household page's gestures on top of
// supportError's.
func passError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, licdomain.ErrNotLifetime):
		response.Error(w, kernelerrors.Conflict(err.Error()))
	case errors.Is(err, licdomain.ErrManualPass), errors.Is(err, licdomain.ErrUnknownPass):
		response.Error(w, kernelerrors.BadRequest(err.Error()))
	default:
		supportError(w, err, "no such account")
	}
}

// frDay is a day as the household reads it; Dakar is UTC all year.
func frDay(t time.Time) string { return t.UTC().Format("02/01/2006") }

// --- renew ------------------------------------------------------------------------

type renewRequest struct {
	BoxID     string `json:"boxId"`
	Tier      string `json:"tier"`
	Days      int    `json:"days"`
	Reason    string `json:"reason"`
	AmountXOF int64  `json:"amountXof"`
}

func (m *Module) renewPass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant, acc, ok := m.householdAccount(w, r)
	if !ok {
		return
	}
	var req renewRequest
	if !decode(w, r, &req) {
		return
	}
	reason, ok := reasonOf(w, req.Reason)
	if !ok {
		return
	}
	tier, ok := parseTier(req.Tier)
	if !ok {
		response.Error(w, kernelerrors.BadRequest("tier: basic or max"))
		return
	}
	if req.Days == 0 {
		req.Days = defaultRenewDays
	}
	if req.Days < 1 || req.Days > licdomain.MaxExtendDays {
		response.Error(w, kernelerrors.BadRequest(fmt.Sprintf("days: 1 to %d", licdomain.MaxExtendDays)))
		return
	}
	if req.AmountXOF < 0 || req.AmountXOF > maxManualAmount {
		response.Error(w, kernelerrors.BadRequest(fmt.Sprintf("amountXof: 0 to %d", maxManualAmount)))
		return
	}
	box, ok := parseBox(w, req.BoxID)
	if !ok {
		return
	}
	before := m.current(ctx, acc.ID, box)
	ref := uuid.NewString()
	sub, err := m.cfg.Support.RenewPass(ctx, licusecase.RenewInput{
		Account: acc.ID, BoxID: string(box), Tier: tier, Days: req.Days, AmountXOF: req.AmountXOF, Reference: ref,
	})
	if err != nil {
		passError(w, err)
		return
	}
	summary := fmt.Sprintf("Pass %s de la box %s renouvelé de %d jour(s), jusqu’au %s", tierName(tier), box, req.Days, day(sub.ExpiresAt))
	if before.Active(m.cfg.Now()) {
		summary += fmt.Sprintf(" (au lieu du %s)", day(before.ExpiresAt))
	}
	if req.AmountXOF > 0 {
		summary += fmt.Sprintf(", %d F reçus", req.AmountXOF)
	} else {
		summary += ", offert"
	}
	details := map[string]any{"box": string(box), "tier": string(tier), "days": req.Days, "amountXof": req.AmountXOF,
		"phone": string(acc.Phone), "reference": ref, "to": sub.ExpiresAt.UTC().Format(time.RFC3339)}
	if !before.ExpiresAt.IsZero() {
		details["from"] = before.ExpiresAt.UTC().Format(time.RFC3339)
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindPassRenewed, TenantID: tenant,
		TargetType: actionlog.TargetAccount, TargetID: string(acc.ID),
		Reason: reason, Summary: summary, Details: details,
	})
	notified := m.notify(ctx, tenant, "Pass prolongé",
		fmt.Sprintf("Votre pass %s est prolongé jusqu’au %s sur la box %s. Merci de votre confiance.",
			tierName(sub.Tier), frDay(sub.ExpiresAt), box))
	response.JSON(w, http.StatusOK, map[string]any{
		"boxId": sub.BoxID, "tier": sub.Tier, "expiresAt": sub.ExpiresAt, "previousExpiresAt": timePtr(before.ExpiresAt),
		"days": req.Days, "amountXof": req.AmountXOF, "reference": ref, "notified": notified,
	})
}

// --- lifetime ---------------------------------------------------------------------

type lifetimeRequest struct {
	BoxID  string `json:"boxId"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

func (m *Module) grantLifetime(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant, acc, ok := m.householdAccount(w, r)
	if !ok {
		return
	}
	var req lifetimeRequest
	if !decode(w, r, &req) {
		return
	}
	reason, ok := reasonOf(w, req.Reason)
	if !ok {
		return
	}
	tier, ok := parseTier(req.Tier)
	if !ok {
		response.Error(w, kernelerrors.BadRequest("tier: basic or max"))
		return
	}
	box, ok := parseBox(w, req.BoxID)
	if !ok {
		return
	}
	before := m.current(ctx, acc.ID, box)
	sub, err := m.cfg.Support.GrantLifetime(ctx, acc.ID, string(box), tier)
	if err != nil {
		passError(w, err)
		return
	}
	summary := fmt.Sprintf("Pass %s à vie accordé sur la box %s", tierName(tier), box)
	switch {
	case before.Lifetime:
		summary += fmt.Sprintf(" (était %s à vie)", tierName(before.Tier))
	case before.Active(m.cfg.Now()):
		summary += fmt.Sprintf(" (pass %s jusqu’au %s mis de côté)", tierName(before.Tier), day(before.ExpiresAt))
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindLifetimeGranted, TenantID: tenant,
		TargetType: actionlog.TargetAccount, TargetID: string(acc.ID),
		Reason: reason, Summary: summary,
		Details: map[string]any{"box": string(box), "tier": string(tier), "phone": string(acc.Phone)},
	})
	notified := m.notify(ctx, tenant, "Pass à vie",
		fmt.Sprintf("L’équipe Kergui vous offre un pass %s à vie sur la box %s. Merci pour votre soutien !", tierName(tier), box))
	response.JSON(w, http.StatusOK, map[string]any{
		"boxId": sub.BoxID, "tier": sub.Tier, "lifetime": true, "notified": notified,
	})
}

type revokeLifetimeRequest struct {
	BoxID  string `json:"boxId"`
	Reason string `json:"reason"`
}

func (m *Module) revokeLifetime(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant, acc, ok := m.householdAccount(w, r)
	if !ok {
		return
	}
	var req revokeLifetimeRequest
	if !decode(w, r, &req) {
		return
	}
	reason, ok := reasonOf(w, req.Reason)
	if !ok {
		return
	}
	box, ok := parseBox(w, req.BoxID)
	if !ok {
		return
	}
	before := m.current(ctx, acc.ID, box)
	sub, err := m.cfg.Support.RevokeLifetime(ctx, acc.ID, string(box))
	if err != nil {
		passError(w, err)
		return
	}
	now := m.cfg.Now()
	active := sub.Active(now)
	summary := fmt.Sprintf("Pass %s à vie retiré de la box %s", tierName(before.Tier), box)
	body := fmt.Sprintf("Votre pass %s à vie sur la box %s a pris fin.", tierName(before.Tier), box)
	if active {
		summary += fmt.Sprintf(" : retour au pass %s jusqu’au %s", tierName(sub.Tier), day(sub.ExpiresAt))
		body += fmt.Sprintf(" Votre pass %s continue jusqu’au %s.", tierName(sub.Tier), frDay(sub.ExpiresAt))
	} else {
		summary += " : la box repasse en Gratuit"
		body += " La box repasse en Gratuit ; vous pouvez reprendre un pass quand vous le souhaitez."
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindLifetimeRevoked, TenantID: tenant,
		TargetType: actionlog.TargetAccount, TargetID: string(acc.ID),
		Reason: reason, Summary: summary,
		Details: map[string]any{"box": string(box), "tier": string(before.Tier), "phone": string(acc.Phone),
			"to": sub.ExpiresAt.UTC().Format(time.RFC3339), "active": active},
	})
	notified := m.notify(ctx, tenant, "Pass à vie retiré", body)
	response.JSON(w, http.StatusOK, map[string]any{
		"boxId": sub.BoxID, "tier": sub.EffectiveTier(now), "expiresAt": sub.ExpiresAt, "active": active, "notified": notified,
	})
}
