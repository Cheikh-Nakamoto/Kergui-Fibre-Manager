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
	"github.com/jackc/pgx/v5"

	licdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// Passes that expire and how well they renew (issue #97), on the « Passes
// et paiements » page.
//
//	GET /api/v1/admin/passes/expiring?status=soon|expired&tier=basic|max&q=&limit=
//	GET /api/v1/admin/passes/renewals?month=2026-11
//
// A pass is one account's subscription on one box (Basic or Max, the trial
// included): what runs on a box and what ends. The expiring list is the
// passes ending within the next 7 days (soon) and those that ended less
// than 30 days ago (expired), soonest end first.
//
// The renewal figures come from the licensing ledger hub_licensing_renewals
// (one row each time a pass got a later end, written by a trigger), read as
// cohorts: the passes whose end date falls in the month. One that got more
// time, before or after that date, is renewed; one that reached it and still
// ends there is lost; one whose end is still ahead is upcoming. The rate is
// renewed / (renewed + lost). The 7-day trial's end is no renewal date: it
// is counted as a trial, converted when the number paid after starting it.

const (
	expiringAhead   = 7 * 24 * time.Hour
	expiredBehind   = 30 * 24 * time.Hour
	expiringDefault = 200
	expiringMax     = 500
	lostMax         = 500
)

// trialEnd tells, in SQL, whether the end date $end of the pass x (with
// account_id and box_id) is the trial's: the trial started on that box and
// the end is within its days. The hour of slack covers the trial's start
// and its pass being written a moment apart.
func trialEnd(x, end string) string {
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM hub_licensing_trials tr
		WHERE tr.account_id = %[1]s.account_id AND tr.box_id = %[1]s.box_id AND tr.started_at IS NOT NULL
		  AND %[2]s <= tr.started_at + interval '%[3]d days 1 hour')`, x, end, licdomain.TrialPass.Days)
}

// lastPayment is the latest pass credited to the number, a gift included.
type lastPayment struct {
	At        time.Time `json:"at"`
	PassCode  string    `json:"passCode"`
	Tier      string    `json:"tier"`
	AmountXOF int64     `json:"amountXof"`
	Provider  string    `json:"provider"`
}

// passRow is one pass of the expiring list and of the lost ones. A later
// column (the reminders sent for this end, issue #99) joins on
// (accountId, boxId, expiresAt).
type passRow struct {
	AccountID string    `json:"accountId"`
	Phone     string    `json:"phone"`
	TenantID  string    `json:"tenantId,omitempty"`
	Household string    `json:"household,omitempty"`
	BoxID     string    `json:"boxId"`
	Tier      string    `json:"tier"`
	ExpiresAt time.Time `json:"expiresAt"`
	Expired   bool      `json:"expired"`
	// Trial is the 7-day Max trial ending, not a pass anyone paid for.
	Trial bool `json:"trial"`
	// Channel is prepaid or play: a Play subscription renews by itself.
	Channel     string       `json:"channel"`
	LastPayment *lastPayment `json:"lastPayment,omitempty"`
}

// passSelect reads passRow; the caller adds WHERE (on s, a) and ORDER BY.
var passSelect = `
	SELECT s.account_id::text, a.phone, COALESCE(h.tenant_id::text, ''), COALESCE(t.name, ''),
	       s.box_id, s.tier, s.expires_at, s.channel, ` + trialEnd("s", "s.expires_at") + `,
	       lp.credited_at, COALESCE(lp.pass_code, ''), COALESCE(lp.amount_xof, 0), COALESCE(lp.provider, '')
	FROM hub_licensing_subscriptions s
	JOIN hub_licensing_accounts a ON a.id = s.account_id
	LEFT JOIN hub_identity_households h ON h.account_id = s.account_id
	LEFT JOIN tenants t ON t.id = h.tenant_id
	LEFT JOIN LATERAL (
	  SELECT p.credited_at, p.pass_code, p.amount_xof, p.provider FROM hub_licensing_payments p
	  WHERE p.account_id = s.account_id AND p.status = 'credited' AND p.credited_at IS NOT NULL
	  ORDER BY p.credited_at DESC LIMIT 1) lp ON true`

func (m *Module) passRows(ctx context.Context, now time.Time, query string, args ...any) ([]passRow, error) {
	rows, err := m.cfg.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []passRow{}
	for rows.Next() {
		var (
			p        passRow
			paidAt   *time.Time
			lp       lastPayment
			provider string
		)
		if err := rows.Scan(&p.AccountID, &p.Phone, &p.TenantID, &p.Household, &p.BoxID, &p.Tier, &p.ExpiresAt,
			&p.Channel, &p.Trial, &paidAt, &lp.PassCode, &lp.AmountXOF, &provider); err != nil {
			return nil, err
		}
		p.Expired = !p.ExpiresAt.After(now)
		if paidAt != nil {
			lp.At, lp.Provider, lp.Tier = *paidAt, provider, passTier(lp.PassCode)
			p.LastPayment = &lp
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// expiring lists the passes ending soon and those ended lately.
func (m *Module) expiring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := m.cfg.Now().UTC()
	q := r.URL.Query()
	from, to := now.Add(-expiredBehind), now.Add(expiringAhead)
	switch q.Get("status") {
	case "":
	case "soon":
		from = now
	case "expired":
		to = now
	default:
		response.Error(w, kernelerrors.BadRequest("status: soon or expired"))
		return
	}
	where := []string{"s.tier <> 'free'", "s.expires_at >= $1", "s.expires_at < $2"}
	args := []any{from, to}
	if tier := q.Get("tier"); tier != "" {
		if tier != string(licdomain.TierBasic) && tier != string(licdomain.TierMax) {
			response.Error(w, kernelerrors.BadRequest("tier: basic or max"))
			return
		}
		args = append(args, tier)
		where = append(where, fmt.Sprintf("s.tier = $%d", len(args)))
	}
	if d := digits(q.Get("q")); d != "" {
		args = append(args, "%"+d+"%")
		where = append(where, fmt.Sprintf(`regexp_replace(a.phone, '\D', '', 'g') LIKE $%d`, len(args)))
	}
	limit := intParam(r, "limit", expiringDefault, expiringMax)
	args = append(args, limit)
	list, err := m.passRows(ctx, now, passSelect+" WHERE "+strings.Join(where, " AND ")+
		fmt.Sprintf(" ORDER BY s.expires_at, s.account_id, s.box_id LIMIT $%d", len(args)), args...)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"now": now, "from": from, "to": to,
		"aheadDays": int(expiringAhead / (24 * time.Hour)), "behindDays": int(expiredBehind / (24 * time.Hour)),
		"passes": list,
	})
}

// --- renewal figures ------------------------------------------------------------

type renewalFigures struct {
	// Due is the passes whose end date is in the month and already passed,
	// plus those renewed ahead of a date still to come: renewed + lost.
	Due      int `json:"due"`
	Renewed  int `json:"renewed"`
	Lost     int `json:"lost"`
	Upcoming int `json:"upcoming"`
	// Rate is renewed / due, absent when nothing came due.
	Rate *float64 `json:"rate,omitempty"`
}

type activeFigures struct {
	Basic int `json:"basic"`
	// Max counts the trials running; MaxTrial is how many of them are.
	Max      int `json:"max"`
	MaxTrial int `json:"maxTrial"`
}

// Converted is a trial whose number paid a pass (money, not a gift) after
// starting it.
type monthTrials struct {
	// Ended is the trials whose 7 days ended in the month, so far.
	Ended     int `json:"ended"`
	Converted int `json:"converted"`
}

type allTrials struct {
	Started   int `json:"started"`
	Converted int `json:"converted"`
}

type renewalsResponse struct {
	Month string    `json:"month"`
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Now   time.Time `json:"now"`
	// TrackedSince is when the hub began recording renewals: a month before
	// it only knows the renewals the expiry reminders saw.
	TrackedSince  *time.Time     `json:"trackedSince,omitempty"`
	Renewal       renewalFigures `json:"renewal"`
	Active        activeFigures  `json:"active"`
	Trials        monthTrials    `json:"trials"`
	TrialsAllTime allTrials      `json:"trialsAllTime"`
	// Lost is the month's lost passes, with whom to call.
	Lost []passRow `json:"lost"`
}

// paidAfter tells, in SQL, whether the trial tr's number paid a pass (real
// money, not a gift) since it started.
var paidAfter = `EXISTS (SELECT 1 FROM hub_licensing_payments p
	WHERE p.account_id = tr.account_id AND p.status = 'credited' AND p.provider <> '` + licusecase.ProviderSupport + `'
	  AND p.amount_xof > 0 AND p.credited_at >= tr.started_at)`

// renewals answers the month's renewal figures, the passes active now and
// the trials. month is YYYY-MM, the current month by default.
func (m *Module) renewals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := m.cfg.Now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if s := r.URL.Query().Get("month"); s != "" {
		t, err := time.Parse("2006-01", s)
		if err != nil {
			response.Error(w, kernelerrors.BadRequest("month: YYYY-MM, e.g. 2026-11"))
			return
		}
		from = t.UTC()
	}
	to := from.AddDate(0, 1, 0)
	reached := to
	if now.Before(reached) {
		reached = now
	}
	out := renewalsResponse{Month: from.Format("2006-01"), From: from, To: to, Now: now, Lost: []passRow{}}

	var since time.Time
	switch err := m.cfg.Pool.QueryRow(ctx, `SELECT started_at FROM hub_licensing_renewals_since`).Scan(&since); {
	case err == nil:
		out.TrackedSince = &since
	case !errors.Is(err, pgx.ErrNoRows):
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}

	// Renewed: each end date in the month that got more time, counted once
	// (a pass renewed twice for the same date, after an expiry moved back,
	// is one renewal). Lost: ended in the month, not renewed since.
	// Upcoming: ends later this month.
	f := &out.Renewal
	err := m.cfg.Pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM (SELECT DISTINCT x.account_id, x.box_id, x.due_at FROM hub_licensing_renewals x
		     WHERE x.due_at >= $1 AND x.due_at < $2 AND NOT `+trialEnd("x", "x.due_at")+`) d),
		  (SELECT count(*) FROM hub_licensing_subscriptions s
		     WHERE s.tier <> 'free' AND s.expires_at >= $1 AND s.expires_at < $3 AND NOT `+trialEnd("s", "s.expires_at")+`),
		  (SELECT count(*) FROM hub_licensing_subscriptions s
		     WHERE s.tier <> 'free' AND s.expires_at >= $3 AND s.expires_at < $2 AND NOT `+trialEnd("s", "s.expires_at")+`)`,
		from, to, reached).Scan(&f.Renewed, &f.Lost, &f.Upcoming)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	f.Due = f.Renewed + f.Lost
	if f.Due > 0 {
		rate := float64(f.Renewed) / float64(f.Due)
		f.Rate = &rate
	}

	err = m.cfg.Pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE s.tier = 'basic'),
		  count(*) FILTER (WHERE s.tier = 'max'),
		  count(*) FILTER (WHERE s.tier = 'max' AND `+trialEnd("s", "s.expires_at")+`)
		FROM hub_licensing_subscriptions s WHERE s.tier <> 'free' AND s.expires_at > $1`, now,
	).Scan(&out.Active.Basic, &out.Active.Max, &out.Active.MaxTrial)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}

	trialDays := fmt.Sprintf("interval '%d days'", licdomain.TrialPass.Days)
	err = m.cfg.Pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE tr.started_at + `+trialDays+` >= $1 AND tr.started_at + `+trialDays+` < $2),
		  count(*) FILTER (WHERE tr.started_at + `+trialDays+` >= $1 AND tr.started_at + `+trialDays+` < $2 AND `+paidAfter+`),
		  count(*),
		  count(*) FILTER (WHERE `+paidAfter+`)
		FROM hub_licensing_trials tr WHERE tr.started_at IS NOT NULL`, from, reached,
	).Scan(&out.Trials.Ended, &out.Trials.Converted, &out.TrialsAllTime.Started, &out.TrialsAllTime.Converted)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}

	out.Lost, err = m.passRows(ctx, now, passSelect+`
		WHERE s.tier <> 'free' AND s.expires_at >= $1 AND s.expires_at < $2 AND NOT `+trialEnd("s", "s.expires_at")+
		fmt.Sprintf(` ORDER BY s.expires_at, s.account_id, s.box_id LIMIT %d`, lostMax), from, reached)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, out)
}
