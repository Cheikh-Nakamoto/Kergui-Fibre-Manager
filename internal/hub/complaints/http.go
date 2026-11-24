package complaints

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	kernelerrors "github.com/agile-factory/kernel/kernel/errors"
	"github.com/agile-factory/kernel/kernel/response"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

type actionlogKind = auditdomain.AuditAction

// Error codes the app and the site read.
const (
	CodeInvalid     = "COMPLAINT_INVALID"
	CodeNotFound    = "COMPLAINT_NOT_FOUND"
	CodeClosed      = "COMPLAINT_CLOSED"
	CodeRateLimited = "COMPLAINT_RATE_LIMITED"
)

// RegisterLicensingRoutes mounts the customer routes behind the licensing
// session. Without a session (a hub without licensing) there are none.
func (m *Module) RegisterLicensingRoutes(mux *http.ServeMux) {
	if m.cfg.Session == nil || m.cfg.Customer == nil {
		return
	}
	const p = "/licensing/v1/complaints"
	mux.Handle("POST "+p, m.cfg.Session(http.HandlerFunc(m.create)))
	mux.Handle("GET "+p, m.cfg.Session(http.HandlerFunc(m.mine)))
	mux.Handle("POST "+p+"/{number}/messages", m.cfg.Session(http.HandlerFunc(m.reply)))
}

// RegisterRoutes mounts the admin routes, behind the JWT middleware.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	const p = "/api/v1/admin/complaints"
	mux.HandleFunc("GET "+p, guard(m.list))
	mux.HandleFunc("GET "+p+"/summary", guard(m.summary))
	mux.HandleFunc("GET "+p+"/{ref}", guard(m.detail))
	mux.HandleFunc("POST "+p+"/{ref}/answer", guard(m.answer))
	mux.HandleFunc("POST "+p+"/{ref}/close", guard(m.close))
	const s = "/api/v1/admin/settings/complaint-admins"
	mux.HandleFunc("GET "+s, guard(m.getAdmins))
	mux.HandleFunc("PUT "+s, guard(m.putAdmins))
}

// guard lets platform administrators through, and nobody else.
func guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if kernelauth.UserIDFromContext(r.Context()) == "" {
			response.Error(w, kernelerrors.Unauthorized("authentication required"))
			return
		}
		if !kernelauth.IsPlatformAdmin(r.Context()) {
			response.Error(w, kernelerrors.Forbidden("platform administrators only"))
			return
		}
		next(w, r)
	}
}

func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		msg := strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": ")
		response.Error(w, kernelerrors.New(CodeInvalid, msg, http.StatusBadRequest, err))
	case errors.Is(err, ErrNotFound):
		response.Error(w, kernelerrors.New(CodeNotFound, "no such complaint", http.StatusNotFound, err))
	case errors.Is(err, ErrClosed):
		response.Error(w, kernelerrors.New(CodeClosed, "the complaint is closed", http.StatusConflict, err))
	case errors.Is(err, ErrRateLimited):
		response.Error(w, kernelerrors.New(CodeRateLimited, "too many complaints or messages, wait a moment", http.StatusTooManyRequests, err))
	default:
		response.Error(w, kernelerrors.WrapDBError(err))
	}
}

func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		response.Error(w, kernelerrors.New(CodeInvalid, "invalid JSON body", http.StatusBadRequest, err))
		return false
	}
	return true
}

// --- the customer -----------------------------------------------------------------

type createRequest struct {
	Category string `json:"category"`
	Message  string `json:"message"`
	Journal  string `json:"journal"`
	// Source is "app" (default) or "web" (the customer area).
	Source string `json:"source"`
}

// customerView is a complaint as its customer sees it: no journal, no
// admin's identity.
func customerView(c Complaint) Complaint {
	v := Complaint{ID: c.ID, Number: c.Number, Source: c.Source, Category: c.Category, Status: c.Status,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, ClosedAt: c.ClosedAt, HasJournal: c.HasJournal,
		Messages: make([]Message, 0, len(c.Messages))}
	for _, msg := range c.Messages {
		v.Messages = append(v.Messages, Message{ID: msg.ID, From: msg.From, Channel: msg.Channel, Body: msg.Body, At: msg.At})
	}
	return v
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	cust, ok := m.cfg.Customer(r.Context())
	if !ok {
		response.Error(w, kernelerrors.Unauthorized("authentication required"))
		return
	}
	var req createRequest
	if !decode(w, r, maxRequest, &req) {
		return
	}
	source := strings.TrimSpace(req.Source)
	switch source {
	case "":
		source = SourceApp
	case SourceApp, SourceWeb:
	default:
		fail(w, invalid("source: app or web"))
		return
	}
	c, err := m.Open(r.Context(), Intake{
		Source: source, Category: req.Category, AccountID: cust.AccountID, Phone: cust.Phone,
		Body: req.Message, Journal: req.Journal,
	})
	if err != nil {
		fail(w, err)
		return
	}
	// The acknowledgement: the number the customer quotes.
	response.JSON(w, http.StatusCreated, map[string]any{
		"id": c.ID, "number": c.Number, "status": c.Status, "category": c.Category, "source": c.Source,
		"createdAt": c.CreatedAt, "hasJournal": c.HasJournal,
	})
}

// mine lists the customer's complaints, newest first, with their thread.
func (m *Module) mine(w http.ResponseWriter, r *http.Request) {
	cust, ok := m.cfg.Customer(r.Context())
	if !ok {
		response.Error(w, kernelerrors.Unauthorized("authentication required"))
		return
	}
	list, err := m.ofCustomer(r.Context(), cust)
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]Complaint, 0, len(list))
	for _, c := range list {
		out = append(out, customerView(c))
	}
	response.JSON(w, http.StatusOK, map[string]any{"complaints": out})
}

// ofCustomer reads a customer's complaints: their account's, and those
// their number sent before it had one.
func (m *Module) ofCustomer(ctx context.Context, cust Customer) ([]Complaint, error) {
	rows, err := m.cfg.Pool.Query(ctx, selectComplaint+`
		WHERE c.account_id = $1::uuid OR c.phone = $2
		ORDER BY c.created_at DESC, c.number DESC LIMIT 100`, cust.AccountID, cust.Phone)
	if err != nil {
		return nil, err
	}
	var list []Complaint
	var ids []string
	for rows.Next() {
		c, err := scanComplaint(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, c)
		ids = append(ids, c.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	threads, err := m.threads(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Messages = threads[list[i].ID]
	}
	return list, nil
}

type replyRequest struct {
	Message string `json:"message"`
	// Source is where the customer wrote it: "app" (default) or "web".
	Source string `json:"source"`
}

// reply adds the customer's message to one of their complaints.
func (m *Module) reply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cust, ok := m.cfg.Customer(ctx)
	if !ok {
		response.Error(w, kernelerrors.Unauthorized("authentication required"))
		return
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(r.PathValue("number"), "#"), 10, 64)
	if err != nil || n <= 0 {
		fail(w, ErrNotFound)
		return
	}
	var req replyRequest
	if !decode(w, r, 64<<10, &req) {
		return
	}
	channel := strings.TrimSpace(req.Source)
	switch channel {
	case "":
		channel = SourceApp
	case SourceApp, SourceWeb:
	default:
		fail(w, invalid("source: app or web"))
		return
	}
	// Only the customer's own: another's number answers as unknown.
	var id string
	err = m.cfg.Pool.QueryRow(ctx, `
		SELECT id::text FROM hub_complaints WHERE number = $1 AND (account_id = $2::uuid OR phone = $3)`,
		n, cust.AccountID, cust.Phone).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, ErrNotFound)
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := m.AddCustomerMessage(ctx, id, channel, req.Message); err != nil {
		fail(w, err)
		return
	}
	c, err := m.Find(ctx, id, false)
	if err != nil {
		fail(w, err)
		return
	}
	response.JSON(w, http.StatusCreated, customerView(c))
}

// --- the back-office ----------------------------------------------------------------

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := Filter{Status: q.Get("status"), Category: q.Get("category"), Source: q.Get("source")}
	switch f.Status {
	case "", StatusOpen, StatusAnswered, StatusClosed:
	default:
		fail(w, invalid("status: open, answered or closed"))
		return
	}
	if f.Category != "" && !ValidCategory(f.Category) {
		fail(w, invalid("category: paiement, box, application or autre"))
		return
	}
	switch f.Source {
	case "", SourceApp, SourceWeb, SourceWhatsApp:
	default:
		fail(w, invalid("source: app, web or whatsapp"))
		return
	}
	if s := q.Get("olderThanHours"); s != "" {
		h, err := strconv.Atoi(s)
		if err != nil || h < 0 {
			fail(w, invalid("olderThanHours: a number of hours"))
			return
		}
		f.OlderThan = time.Duration(h) * time.Hour
	}
	if s := strings.TrimSpace(q.Get("phone")); s != "" {
		p, err := alertsdomain.ParsePhone(s)
		if err != nil {
			fail(w, invalid("phone: not a phone number"))
			return
		}
		f.Phone = string(p)
	}
	if s := q.Get("limit"); s != "" {
		f.Limit, _ = strconv.Atoi(s)
	}
	list, err := m.List(r.Context(), f)
	if err != nil {
		fail(w, err)
		return
	}
	counts, err := m.Count(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"complaints": list, "counts": counts})
}

func (m *Module) summary(w http.ResponseWriter, r *http.Request) {
	counts, err := m.Count(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	response.JSON(w, http.StatusOK, counts)
}

func (m *Module) detail(w http.ResponseWriter, r *http.Request) {
	c, err := m.Find(r.Context(), r.PathValue("ref"), true)
	if err != nil {
		fail(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"complaint": c, "limits": map[string]int{"message": MaxMessage}})
}

func (m *Module) answer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Message string `json:"message"`
	}
	if !decode(w, r, 64<<10, &req) {
		return
	}
	a, err := m.Answer(ctx, r.PathValue("ref"), req.Message, kernelauth.UserIDFromContext(ctx))
	if err != nil {
		fail(w, err)
		return
	}
	wa := map[string]string{
		WhatsAppQueued: "WhatsApp en file", WhatsAppNotOptedIn: "pas de WhatsApp (non activé)", WhatsAppOff: "WhatsApp non configuré",
	}[a.Delivery.WhatsApp]
	if wa == "" {
		wa = "pas de WhatsApp"
	}
	m.record(ctx, actionlog.KindComplaintAnswered, a.Complaint,
		fmt.Sprintf("Réponse à la plainte n° %d (%s, %s) : %d téléphone(s), %s",
			a.Complaint.Number, a.Complaint.Category, a.Complaint.Phone, a.Delivery.Pushed, wa),
		map[string]any{"number": a.Complaint.Number, "category": a.Complaint.Category, "source": a.Complaint.Source,
			"messageId": a.Message.ID, "pushed": a.Delivery.Pushed, "pushFailed": a.Delivery.PushFailed,
			"whatsapp": a.Delivery.WhatsApp})
	response.JSON(w, http.StatusCreated, a)
}

func (m *Module) close(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := m.Close(ctx, r.PathValue("ref"), kernelauth.UserIDFromContext(ctx))
	if err != nil {
		fail(w, err)
		return
	}
	m.record(ctx, actionlog.KindComplaintClosed, c,
		fmt.Sprintf("Plainte n° %d close (%s, %s)", c.Number, c.Category, c.Phone),
		map[string]any{"number": c.Number, "category": c.Category, "source": c.Source})
	response.JSON(w, http.StatusOK, map[string]any{"complaint": c})
}

// --- the admins' WhatsApp numbers ----------------------------------------------------

// AdminNumbers are the admins' WhatsApp numbers (E.164), who get the
// complaints' summary and may send the commands (issue #83).
func (m *Module) AdminNumbers(ctx context.Context) ([]string, error) {
	var raw []byte
	err := m.cfg.Pool.QueryRow(ctx, `SELECT value FROM hub_settings WHERE key = $1`, SettingAdmins).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// IsAdminNumber reports whether phone is one of the admins' numbers.
func (m *Module) IsAdminNumber(ctx context.Context, phone alertsdomain.Phone) (bool, error) {
	list, err := m.AdminNumbers(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range list {
		if n == string(phone) {
			return true, nil
		}
	}
	return false, nil
}

func (m *Module) getAdmins(w http.ResponseWriter, r *http.Request) {
	list, err := m.AdminNumbers(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	response.JSON(w, http.StatusOK, map[string]any{"numbers": list, "max": MaxAdmins})
}

func (m *Module) putAdmins(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req struct {
		Numbers []string `json:"numbers"`
	}
	if !decode(w, r, 16<<10, &req) {
		return
	}
	numbers := []string{}
	seen := map[string]bool{}
	for _, raw := range req.Numbers {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := alertsdomain.ParsePhone(raw)
		if err != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{
				"error":   map[string]string{"code": CodeInvalid, "message": "numbers: not a phone number: " + raw},
				"invalid": raw,
			})
			return
		}
		if seen[string(p)] {
			continue
		}
		seen[string(p)] = true
		numbers = append(numbers, string(p))
	}
	if len(numbers) > MaxAdmins {
		fail(w, invalid("numbers: at most %d", MaxAdmins))
		return
	}
	before, err := m.AdminNumbers(ctx)
	if err != nil {
		fail(w, err)
		return
	}
	value, _ := json.Marshal(numbers)
	var by *string
	if id := kernelauth.UserIDFromContext(ctx); id != "" {
		by = &id
	}
	if _, err := m.cfg.Pool.Exec(ctx, `
		INSERT INTO hub_settings (key, value, updated_at, updated_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by`,
		SettingAdmins, value, m.cfg.Now().UTC(), by); err != nil {
		fail(w, err)
		return
	}
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: actionlog.KindComplaintAdminsSet, TargetType: actionlog.TargetSettings,
		Summary: fmt.Sprintf("Numéros WhatsApp des admins (plaintes) : %d numéro(s)", len(numbers)),
		Details: map[string]any{"setting": SettingAdmins, "numbers": numbers, "before": before},
	})
	response.JSON(w, http.StatusOK, map[string]any{"numbers": numbers, "max": MaxAdmins})
}
