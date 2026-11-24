package admin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	licdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	licusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// The « Paiements » page (issue #79, parent #67): what came in, what is
// stuck at NabooPay, a re-check of one order, and an export for the
// accounts.
//
//	GET  /api/v1/admin/payments/revenue?from=&to=&by=day|month
//	GET  /api/v1/admin/payments/stuck?olderThan=10m&failedDays=7
//	POST /api/v1/admin/payments/{paymentId}/recheck   {reason?}
//	GET  /api/v1/admin/payments/export.csv?from=&to=
//
// from and to are days (2006-01-02, to included) or RFC 3339 times; Dakar
// is UTC all year, so a day is a UTC day. Revenue counts credited payments
// only, by the day they were credited, and leaves out the passes support
// gave (0 F): it is money. The export lists every payment created in the
// range, whatever its status, with the full number: an internal tool.

// Rechecker asks NabooPay about one order and credits it once if paid: the
// licensing module's SettleOrder. credited is true when this call did.
type Rechecker func(ctx context.Context, orderID string) (credited bool, err error)

const (
	defaultStuckAfter = 10 * time.Minute
	defaultFailedDays = 7
	stuckLimit        = 200
	maxRevenueDays    = 400
	maxRevenueMonths  = 120
	maxExportDays     = 731
)

// --- revenue ----------------------------------------------------------------

type figures struct {
	AmountXOF int64 `json:"amountXof"`
	Count     int64 `json:"count"`
}

func (f *figures) add(amount, count int64) { f.AmountXOF += amount; f.Count += count }

type passFigures struct {
	Pass string `json:"pass"`
	Tier string `json:"tier"`
	figures
}

type tierFigures struct {
	Tier string `json:"tier"`
	figures
}

type period struct {
	Period string `json:"period"`
	figures
	ByPass []passFigures `json:"byPass"`
	ByTier []tierFigures `json:"byTier"`
}

type revenueResponse struct {
	From    time.Time     `json:"from"`
	To      time.Time     `json:"to"`
	By      string        `json:"by"`
	Totals  figures       `json:"totals"`
	Periods []period      `json:"periods"`
	ByPass  []passFigures `json:"byPass"`
	ByTier  []tierFigures `json:"byTier"`
}

// breakdown sums figures by pass and by tier, in a stable order.
type breakdown struct {
	pass map[string]*passFigures
	tier map[string]*tierFigures
}

func newBreakdown() breakdown {
	return breakdown{pass: map[string]*passFigures{}, tier: map[string]*tierFigures{}}
}

func (b breakdown) add(code string, amount, count int64) {
	tier := passTier(code)
	if b.pass[code] == nil {
		b.pass[code] = &passFigures{Pass: code, Tier: tier}
	}
	b.pass[code].add(amount, count)
	if b.tier[tier] == nil {
		b.tier[tier] = &tierFigures{Tier: tier}
	}
	b.tier[tier].add(amount, count)
}

func (b breakdown) lists() ([]passFigures, []tierFigures) {
	passes := make([]passFigures, 0, len(b.pass))
	for _, p := range b.pass {
		passes = append(passes, *p)
	}
	sort.Slice(passes, func(i, j int) bool {
		if passes[i].AmountXOF != passes[j].AmountXOF {
			return passes[i].AmountXOF > passes[j].AmountXOF
		}
		return passes[i].Pass < passes[j].Pass
	})
	tiers := make([]tierFigures, 0, len(b.tier))
	for _, t := range b.tier {
		tiers = append(tiers, *t)
	}
	sort.Slice(tiers, func(i, j int) bool {
		if tiers[i].AmountXOF != tiers[j].AmountXOF {
			return tiers[i].AmountXOF > tiers[j].AmountXOF
		}
		return tiers[i].Tier < tiers[j].Tier
	})
	return passes, tiers
}

func (m *Module) revenue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	by := r.URL.Query().Get("by")
	if by == "" {
		by = "day"
	}
	if by != "day" && by != "month" {
		response.Error(w, kernelerrors.BadRequest("by: day or month"))
		return
	}
	now := m.cfg.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if by == "month" {
		start = start.AddDate(0, -11, 0)
	}
	from, to, ok := dateRange(w, r, start, now)
	if !ok {
		return
	}
	if (by == "day" && to.Sub(from) > maxRevenueDays*24*time.Hour) ||
		(by == "month" && to.After(from.AddDate(0, maxRevenueMonths, 0))) {
		response.Error(w, kernelerrors.BadRequest("range too long: switch to by=month or narrow it"))
		return
	}
	layout := "YYYY-MM-DD"
	if by == "month" {
		layout = "YYYY-MM"
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT to_char(credited_at AT TIME ZONE 'UTC', $3) AS period, pass_code, sum(amount_xof)::bigint, count(*)
		FROM hub_licensing_payments
		WHERE status = 'credited' AND provider <> $4 AND credited_at >= $1 AND credited_at < $2
		GROUP BY 1, 2 ORDER BY 1, 2`, from, to, layout, licusecase.ProviderSupport)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer rows.Close()
	out := revenueResponse{From: from, To: to, By: by, Periods: []period{}}
	all := newBreakdown()
	var (
		order []string
		per   = map[string]*period{}
		split = map[string]breakdown{}
	)
	for rows.Next() {
		var key, code string
		var amount, count int64
		if err := rows.Scan(&key, &code, &amount, &count); err != nil {
			response.Error(w, kernelerrors.WrapDBError(err))
			return
		}
		if per[key] == nil {
			per[key], split[key] = &period{Period: key}, newBreakdown()
			order = append(order, key)
		}
		per[key].add(amount, count)
		split[key].add(code, amount, count)
		all.add(code, amount, count)
		out.Totals.add(amount, count)
	}
	if err := rows.Err(); err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	for _, key := range order {
		p := per[key]
		p.ByPass, p.ByTier = split[key].lists()
		out.Periods = append(out.Periods, *p)
	}
	out.ByPass, out.ByTier = all.lists()
	response.JSON(w, http.StatusOK, out)
}

// --- stuck and failed ---------------------------------------------------------

type watchRow struct {
	paymentRow
	Tier     string `json:"tier"`
	TenantID string `json:"tenantId,omitempty"`
	// Recheckable is a NabooPay order still pending: what « Revérifier »
	// can settle.
	Recheckable bool `json:"recheckable"`
}

const watchSelect = `
	SELECT p.id, p.provider, p.provider_ref, p.account_id, a.phone, COALESCE(p.box_id, ''), p.pass_code,
	       p.amount_xof, p.status, p.created_at, p.credited_at, COALESCE(h.tenant_id::text, '')
	FROM hub_licensing_payments p
	JOIN hub_licensing_accounts a ON a.id = p.account_id
	LEFT JOIN hub_identity_households h ON h.account_id = p.account_id`

func scanWatch(row pgx.Row) (watchRow, error) {
	var p watchRow
	err := row.Scan(&p.ID, &p.Provider, &p.Reference, &p.AccountID, &p.Phone, &p.BoxID, &p.PassCode,
		&p.AmountXOF, &p.Status, &p.CreatedAt, &p.Credited, &p.TenantID)
	p.Tier = passTier(p.PassCode)
	p.Recheckable = p.Provider == licusecase.ProviderNabooPay && p.Status == string(licusecase.PaymentPending)
	return p, err
}

func (m *Module) watchList(ctx context.Context, where string, args ...any) ([]watchRow, error) {
	rows, err := m.cfg.Pool.Query(ctx, watchSelect+" WHERE "+where+fmt.Sprintf(" ORDER BY p.created_at DESC LIMIT %d", stuckLimit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []watchRow{}
	for rows.Next() {
		p, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (m *Module) stuck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	after := defaultStuckAfter
	if s := r.URL.Query().Get("olderThan"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < time.Minute || d > 30*24*time.Hour {
			response.Error(w, kernelerrors.BadRequest("olderThan: a duration from 1m to 720h, e.g. 10m"))
			return
		}
		after = d
	}
	days := intParam(r, "failedDays", defaultFailedDays, 90)
	now := m.cfg.Now()
	pending, err := m.watchList(ctx, "p.status = 'pending' AND p.created_at < $1", now.Add(-after))
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	failed, err := m.watchList(ctx, "p.status = 'failed' AND p.created_at >= $1", now.AddDate(0, 0, -days))
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{
		"olderThanMinutes": int(after / time.Minute), "failedDays": days,
		"pending": pending, "failed": failed, "recheck": m.cfg.Recheck != nil,
	})
}

// --- re-check -------------------------------------------------------------------

type recheckRequest struct {
	Reason string `json:"reason"`
}

// recheck asks NabooPay about one order, through the settle use case the
// webhook and the sweep use: a paid order whose webhook got lost is
// credited, once.
func (m *Module) recheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("paymentId")
	if _, err := uuid.Parse(id); err != nil {
		response.Error(w, kernelerrors.BadRequest("not a payment id"))
		return
	}
	var req recheckRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		response.Error(w, kernelerrors.BadRequest("invalid JSON body"))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if len(reason) > 500 {
		response.Error(w, kernelerrors.BadRequest("reason: 500 characters at most"))
		return
	}
	before, err := scanWatch(m.cfg.Pool.QueryRow(ctx, watchSelect+` WHERE p.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		response.Error(w, kernelerrors.NotFound("no such payment"))
		return
	}
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	if before.Provider != licusecase.ProviderNabooPay {
		response.Error(w, kernelerrors.BadRequest("not a NabooPay order: only those can be re-checked at NabooPay"))
		return
	}
	if m.cfg.Recheck == nil {
		response.Error(w, kernelerrors.ServiceUnavailable("NabooPay is not configured on this hub"))
		return
	}
	credited, err := m.cfg.Recheck(ctx, before.Reference)
	if err != nil {
		response.Error(w, kernelerrors.New("BAD_GATEWAY", "NabooPay did not answer: "+err.Error(), http.StatusBadGateway, err))
		return
	}
	after, err := scanWatch(m.cfg.Pool.QueryRow(ctx, watchSelect+` WHERE p.id = $1`, id))
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	summary := fmt.Sprintf("Paiement NabooPay %s (%s, %s F) revérifié : %s → %s", before.Reference, before.PassCode,
		strconv.FormatInt(before.AmountXOF, 10), before.Status, after.Status)
	if credited {
		summary += ", pass crédité"
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindPaymentRechecked, TenantID: before.TenantID,
		TargetType: actionlog.TargetPayment, TargetID: id,
		Reason: reason, Summary: summary,
		Details: map[string]any{"reference": before.Reference, "phone": before.Phone, "account": before.AccountID,
			"from": before.Status, "to": after.Status, "credited": credited},
	})
	response.JSON(w, http.StatusOK, map[string]any{
		"paymentId": id, "reference": before.Reference, "previousStatus": before.Status,
		"status": after.Status, "credited": credited, "creditedAt": after.Credited,
	})
}

// --- CSV export -----------------------------------------------------------------

var exportHeader = []string{"created_at", "credited_at", "reference", "provider", "phone", "box", "pass", "tier", "amount_xof", "status"}

// exportCSV streams the payments created in the range, oldest first, as
// UTF-8 CSV with a byte order mark so that Excel reads the accents.
func (m *Module) exportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := m.cfg.Now().UTC()
	from, to, ok := dateRange(w, r, time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC), now)
	if !ok {
		return
	}
	if to.Sub(from) > maxExportDays*24*time.Hour {
		response.Error(w, kernelerrors.BadRequest(fmt.Sprintf("range too long: %d days at most", maxExportDays)))
		return
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT p.created_at, p.credited_at, p.provider_ref, p.provider, a.phone, COALESCE(p.box_id, ''),
		       p.pass_code, p.amount_xof, p.status
		FROM hub_licensing_payments p JOIN hub_licensing_accounts a ON a.id = p.account_id
		WHERE p.created_at >= $1 AND p.created_at < $2
		ORDER BY p.created_at, p.id`, from, to)
	if err != nil {
		response.Error(w, kernelerrors.WrapDBError(err))
		return
	}
	defer rows.Close()

	last := to.Add(-time.Nanosecond)
	name := fmt.Sprintf("kergui-paiements-%s-au-%s.csv", day(from), day(last))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "\uFEFF")
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	_ = cw.Write(exportHeader)
	flusher, _ := w.(http.Flusher)
	n := 0
	for rows.Next() {
		var (
			created                                 time.Time
			credited                                *time.Time
			ref, provider, phone, box, code, status string
			amount                                  int64
		)
		if err := rows.Scan(&created, &credited, &ref, &provider, &phone, &box, &code, &amount, &status); err != nil {
			// The status line is gone: the file ends short, the log says why.
			return
		}
		creditedAt := ""
		if credited != nil {
			creditedAt = credited.UTC().Format(time.RFC3339)
		}
		if err := cw.Write([]string{
			created.UTC().Format(time.RFC3339), creditedAt, cell(ref), provider, phone, cell(box),
			cell(code), passTier(code), strconv.FormatInt(amount, 10), status,
		}); err != nil {
			return
		}
		if n++; n%200 == 0 {
			cw.Flush()
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	cw.Flush()
}

// cell keeps a spreadsheet from reading a value as a formula. The phone
// numbers' leading + is ours and left alone.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// --- helpers ----------------------------------------------------------------------

// dateRange reads from and to (to included), with defaults, answering 400
// itself.
func dateRange(w http.ResponseWriter, r *http.Request, defFrom, defTo time.Time) (time.Time, time.Time, bool) {
	from, to := defFrom, defTo
	var err error
	if s := r.URL.Query().Get("from"); s != "" {
		if from, err = parseDay(s, false); err != nil {
			response.Error(w, kernelerrors.BadRequest("from: a day (2006-01-02) or an RFC 3339 time"))
			return time.Time{}, time.Time{}, false
		}
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if to, err = parseDay(s, true); err != nil {
			response.Error(w, kernelerrors.BadRequest("to: a day (2006-01-02) or an RFC 3339 time"))
			return time.Time{}, time.Time{}, false
		}
	}
	if !from.Before(to) {
		response.Error(w, kernelerrors.BadRequest("from must be before to"))
		return time.Time{}, time.Time{}, false
	}
	return from.UTC(), to.UTC(), true
}

// passTier is the tier a pass code or a Play product id sells; "" when the
// hub no longer knows it.
func passTier(code string) string {
	if p, err := licdomain.PassByCode(code); err == nil {
		return string(p.Tier)
	}
	if t, err := licdomain.PlayProductTier(code); err == nil {
		return string(t)
	}
	return ""
}
