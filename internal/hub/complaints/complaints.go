// Package complaints is the hub's complaints module (issue #82, parent #68):
// a household tells Kergui what is wrong, gets a complaint number at once,
// and the Kergui team answers from the back-office.
//
// A complaint has a readable number (n° 12), a source (app, web, or
// whatsapp from issue #83), a category (paiement, box, application, autre;
// the app sends « compte » for paiement, #118),
// the customer (licensing account and number), the household the account
// owns when it has one, a status (open → answered → closed; a customer's
// new message on an answered complaint opens it again), the app's journal
// when the customer attached it (capped at 64 KiB, the recent end kept),
// and a thread of messages from the customer and from the team.
//
// Customer routes, behind the licensing session (kgs_), which the app and
// the customer area on kergui.click both hold:
//
//	POST /licensing/v1/complaints                    {category, message, journal?, source?} → 201 {id, number, status, createdAt}
//	GET  /licensing/v1/complaints                    the customer's complaints with their thread
//	POST /licensing/v1/complaints/{number}/messages  {message} → 201 the complaint
//
// Admin routes, platform administrators only (the kernel JWT's ROLE_ADMIN):
//
//	GET  /api/v1/admin/complaints?status=&category=&source=&olderThanHours=&phone=&limit=
//	GET  /api/v1/admin/complaints/summary            the counts, for the menu
//	GET  /api/v1/admin/complaints/{ref}              ref: the id or the number; the thread and the journal
//	POST /api/v1/admin/complaints/{ref}/answer       {message} → the answer and where it went
//	POST /api/v1/admin/complaints/{ref}/close
//	GET  /api/v1/admin/settings/complaint-admins     {numbers}
//	PUT  /api/v1/admin/settings/complaint-admins     {numbers} (any form the sign-in accepts)
//
// An answer reaches the customer by push on every phone of the account,
// its data {kind: "reply", complaintId, number, category, status, title,
// body, tenantId} filed by the app in its notifications (issue #84), and by
// WhatsApp when the complaint came in by WhatsApp or the customer opted
// in, through the back-office's WhatsApp queue and its 40 s pace (#81).
// Every admin write lands in the action log.
//
// On WhatsApp (issue #83, whatsapp.go): Receive takes what the alerts'
// grammar did not match — a customer's complaint, or an admin's LISTE,
// VOIR, R and OK — and Run sends the admins' summary of new complaints.
package complaints

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agile-factory/kernel/platform/audit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/messages"
)

// Migrations holds the module's tables.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// Sources: where a complaint came from.
const (
	SourceApp      = "app"
	SourceWeb      = "web"
	SourceWhatsApp = "whatsapp"
)

// Categories.
const (
	CategoryPayment = "paiement"
	CategoryBox     = "box"
	CategoryApp     = "application"
	CategoryOther   = "autre"
	// CategoryAccount is the Play app's name for CategoryPayment (« Mon
	// compte », #118): the app names no payment (RFC-006). It is filed as
	// CategoryPayment, so the team sees one category.
	CategoryAccount = "compte"
)

// Statuses.
const (
	StatusOpen     = "open"
	StatusAnswered = "answered"
	StatusClosed   = "closed"
)

// Who wrote a message of the thread.
const (
	FromCustomer = "customer"
	FromAdmin    = "admin"
)

// ChannelBackOffice is an answer written in the back-office; the other
// channels are the sources.
const ChannelBackOffice = "backoffice"

// What happened to an answer's WhatsApp copy.
const (
	WhatsAppQueued     = "queued"
	WhatsAppNotOptedIn = "not_opted_in"
	WhatsAppOff        = "off"
)

// Limits.
const (
	// MaxMessage is a message of the thread, in characters.
	MaxMessage = 2000
	// MaxJournal is the attached journal, in bytes; past it the most
	// recent end is kept.
	MaxJournal = 64 << 10
	// maxRequest bounds a customer's request: a message and a journal the
	// app may not have trimmed.
	maxRequest = 512 << 10
	// MaxAdmins caps the admins' WhatsApp numbers.
	MaxAdmins = 10
	// DefaultNewPerHour and DefaultMessagesPerHour are a customer's budget:
	// new complaints, and messages on their complaints, per rolling hour.
	DefaultNewPerHour      = 5
	DefaultMessagesPerHour = 30
	// DefaultSummaryInterval is the admins' WhatsApp summary pace.
	DefaultSummaryInterval = 15 * time.Minute
)

// PushKind is the push data's kind: what the app files as an answer.
const PushKind = "reply"

// SettingAdmins is the hub setting holding the admins' WhatsApp numbers.
const SettingAdmins = "complaint_admins"

// SettingSummary is the hub setting holding when the admins' last WhatsApp
// summary went: {"at": RFC 3339}.
const SettingSummary = "complaint_summary"

// Customer is who the licensing session says is calling.
type Customer struct {
	AccountID string
	Phone     string
}

// WhatsAppQueue is the back-office's WhatsApp queue (the messages module).
type WhatsAppQueue interface {
	EnqueueWhatsApp(ctx context.Context, d messages.Direct) error
}

// Config wires the module.
type Config struct {
	Pool *pgxpool.Pool
	// Session resolves the licensing session (kgs_) and Customer reads
	// what it resolved. Nil Session leaves the customer routes off (a hub
	// without licensing).
	Session  func(http.Handler) http.Handler
	Customer func(ctx context.Context) (Customer, bool)
	// Pusher is the alerts' push sender; nil sends answers by no push.
	Pusher alertsdomain.Pusher
	// Targets forgets a token the provider says is gone. Nil leaves it.
	Targets alertsdomain.PushTargets
	// WhatsApp is the back-office's paced queue; nil sends no WhatsApp.
	WhatsApp WhatsAppQueue
	// NewPerHour and MessagesPerHour are a customer's budget (defaults 5
	// and 30).
	NewPerHour      int
	MessagesPerHour int
	// SummaryInterval is the admins' WhatsApp summary pace (issue #83): at
	// most one summary per interval, only when complaints came in (default
	// 15 min).
	SummaryInterval time.Duration
	Audit           audit.EventLogger
	Logger          *slog.Logger
	Now             func() time.Time
}

// Module serves the routes.
type Module struct {
	cfg Config
	// intake serialises the WhatsApp intake: whatsmeow hands each message
	// to its own goroutine, and « Bonjour » then « ma box ne marche pas »
	// one second apart must make one complaint, not two.
	intake sync.Mutex
}

// New wires the module.
func New(cfg Config) *Module {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewPerHour <= 0 {
		cfg.NewPerHour = DefaultNewPerHour
	}
	if cfg.MessagesPerHour <= 0 {
		cfg.MessagesPerHour = DefaultMessagesPerHour
	}
	if cfg.SummaryInterval <= 0 {
		cfg.SummaryInterval = DefaultSummaryInterval
	}
	return &Module{cfg: cfg}
}

// --- the model ------------------------------------------------------------------

// Household is the household a complaint is about.
type Household struct {
	TenantID string `json:"tenantId"`
	Name     string `json:"name"`
}

// Author is the admin who wrote an answer.
type Author struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
}

// Message is one message of the thread.
type Message struct {
	ID      int64     `json:"id"`
	From    string    `json:"from"`
	Channel string    `json:"channel"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
	// Admin side only: the author and what reached the customer.
	Author     *Author `json:"author,omitempty"`
	Pushed     *int    `json:"pushed,omitempty"`
	PushFailed *int    `json:"pushFailed,omitempty"`
	// WhatsApp is the answer's WhatsApp copy: "" none, queued (then the
	// queue's own status: sent, failed), not_opted_in, off.
	WhatsApp string `json:"whatsapp,omitempty"`
}

// Complaint is one complaint.
type Complaint struct {
	ID        string     `json:"id"`
	Number    int64      `json:"number"`
	Source    string     `json:"source"`
	Category  string     `json:"category"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	ClosedAt  *time.Time `json:"closedAt,omitempty"`
	// HasJournal says the app's journal is attached.
	HasJournal bool `json:"hasJournal"`

	// Admin side.
	AccountID    string     `json:"accountId,omitempty"`
	Phone        string     `json:"phone,omitempty"`
	Household    *Household `json:"household,omitempty"`
	JournalBytes int        `json:"journalBytes,omitempty"`
	Journal      *string    `json:"journal,omitempty"`
	ClosedBy     *Author    `json:"closedBy,omitempty"`
	// List entries: the first message's start, the thread's size and its
	// last message.
	Preview       string     `json:"preview,omitempty"`
	MessageCount  int        `json:"messageCount,omitempty"`
	LastFrom      string     `json:"lastFrom,omitempty"`
	LastMessageAt *time.Time `json:"lastMessageAt,omitempty"`

	Messages []Message `json:"messages,omitempty"`

	tenantID string
}

// Errors.
var (
	ErrNotFound    = errors.New("complaints: no such complaint")
	ErrClosed      = errors.New("complaints: the complaint is closed")
	ErrRateLimited = errors.New("complaints: too many complaints or messages, wait a moment")
	ErrInvalid     = errors.New("complaints: invalid")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// --- intake ---------------------------------------------------------------------

// Intake is a new complaint.
type Intake struct {
	Source   string
	Category string
	// AccountID is the signed-in customer's account; empty for a WhatsApp
	// sender, then Phone alone says who (and finds the account if any).
	AccountID string
	Phone     string
	Body      string
	Journal   string
}

// NormalizeCategory trims and lowers c, and files the app's CategoryAccount
// under CategoryPayment.
func NormalizeCategory(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if c == CategoryAccount {
		return CategoryPayment
	}
	return c
}

// ValidCategory reports whether c is a category.
func ValidCategory(c string) bool {
	switch c {
	case CategoryPayment, CategoryBox, CategoryApp, CategoryOther:
		return true
	}
	return false
}

// cleanText drops what Postgres refuses and what is not text.
func cleanText(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "")
}

// cleanBody checks a message of the thread.
func cleanBody(s string) (string, error) {
	s = strings.TrimSpace(cleanText(s))
	if s == "" || utf8.RuneCountInString(s) > MaxMessage {
		return "", invalid("message: 1 to %d characters", MaxMessage)
	}
	return s, nil
}

// capJournal keeps the most recent MaxJournal bytes, on a character
// boundary.
func capJournal(s string) string {
	s = cleanText(s)
	if len(s) <= MaxJournal {
		return s
	}
	s = s[len(s)-MaxJournal:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

// Open stores a new complaint with its first message.
func (m *Module) Open(ctx context.Context, in Intake) (Complaint, error) {
	var c Complaint
	switch in.Source {
	case SourceApp, SourceWeb, SourceWhatsApp:
	default:
		return c, invalid("source: app, web or whatsapp")
	}
	category := NormalizeCategory(in.Category)
	if !ValidCategory(category) {
		return c, invalid("category: paiement (compte), box, application or autre")
	}
	body, err := cleanBody(in.Body)
	if err != nil {
		return c, err
	}
	phone, err := alertsdomain.ParsePhone(in.Phone)
	if err != nil {
		return c, invalid("phone: not a phone number")
	}
	journal := capJournal(in.Journal)
	now := m.cfg.Now().UTC()

	tx, err := m.cfg.Pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Who: the account (given, or found by the number) and its household.
	var account, tenant *string
	q := `SELECT a.id::text, h.tenant_id::text FROM hub_licensing_accounts a
	      LEFT JOIN hub_identity_households h ON h.account_id = a.id WHERE `
	var arg string
	if in.AccountID != "" {
		q, arg = q+`a.id = $1::uuid`, in.AccountID
	} else {
		q, arg = q+`a.phone = $1`, string(phone)
	}
	if err := tx.QueryRow(ctx, q, arg).Scan(&account, &tenant); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	if in.AccountID != "" && account == nil {
		return c, invalid("account: unknown")
	}

	// The customer's budget.
	var recent int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM hub_complaints WHERE phone = $1 AND created_at > $2`,
		string(phone), now.Add(-time.Hour)).Scan(&recent); err != nil {
		return c, err
	}
	if recent >= m.cfg.NewPerHour {
		return c, ErrRateLimited
	}

	c = Complaint{ID: uuid.NewString(), Source: in.Source, Category: category, Status: StatusOpen,
		CreatedAt: now, UpdatedAt: now, HasJournal: journal != ""}
	if err := tx.QueryRow(ctx, `
		INSERT INTO hub_complaints (id, source, category, account_id, phone, tenant_id, journal, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8) RETURNING number`,
		c.ID, c.Source, c.Category, account, string(phone), tenant, journal, now).Scan(&c.Number); err != nil {
		return c, err
	}
	msg := Message{From: FromCustomer, Channel: in.Source, Body: body, At: now}
	if err := tx.QueryRow(ctx, `
		INSERT INTO hub_complaint_messages (complaint_id, sender, channel, body, created_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, c.ID, msg.From, msg.Channel, msg.Body, now).Scan(&msg.ID); err != nil {
		return c, err
	}
	if err := tx.Commit(ctx); err != nil {
		return c, err
	}
	c.Messages = []Message{msg}
	if m.cfg.Logger != nil {
		m.cfg.Logger.Info("complaints: new complaint", "number", c.Number, "source", c.Source, "category", c.Category,
			"from", phone.Masked())
	}
	return c, nil
}

// AddCustomerMessage adds the customer's message to a complaint and opens
// it again if it was answered. A closed complaint takes no more.
func (m *Module) AddCustomerMessage(ctx context.Context, complaintID, channel, text string) (Message, error) {
	var msg Message
	body, err := cleanBody(text)
	if err != nil {
		return msg, err
	}
	now := m.cfg.Now().UTC()
	tx, err := m.cfg.Pool.Begin(ctx)
	if err != nil {
		return msg, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status, phone string
	if err := tx.QueryRow(ctx, `SELECT status, phone FROM hub_complaints WHERE id = $1 FOR UPDATE`, complaintID).
		Scan(&status, &phone); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return msg, ErrNotFound
		}
		return msg, err
	}
	if status == StatusClosed {
		return msg, ErrClosed
	}
	var recent int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM hub_complaint_messages cm JOIN hub_complaints c ON c.id = cm.complaint_id
		WHERE c.phone = $1 AND cm.sender = 'customer' AND cm.created_at > $2`,
		phone, now.Add(-time.Hour)).Scan(&recent); err != nil {
		return msg, err
	}
	if recent >= m.cfg.MessagesPerHour {
		return msg, ErrRateLimited
	}
	msg = Message{From: FromCustomer, Channel: channel, Body: body, At: now}
	if err := tx.QueryRow(ctx, `
		INSERT INTO hub_complaint_messages (complaint_id, sender, channel, body, created_at)
		VALUES ($1, 'customer', $2, $3, $4) RETURNING id`, complaintID, channel, body, now).Scan(&msg.ID); err != nil {
		return msg, err
	}
	if _, err := tx.Exec(ctx, `UPDATE hub_complaints SET status = 'open', updated_at = $2 WHERE id = $1`, complaintID, now); err != nil {
		return msg, err
	}
	return msg, tx.Commit(ctx)
}

// --- reading --------------------------------------------------------------------

// resolveRef reads a complaint's id or number ("12", "#12", a UUID).
func (m *Module) resolveRef(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	if _, err := uuid.Parse(ref); err == nil {
		return ref, nil
	}
	n, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || n <= 0 {
		return "", ErrNotFound
	}
	var id string
	if err := m.cfg.Pool.QueryRow(ctx, `SELECT id::text FROM hub_complaints WHERE number = $1`, n).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return id, nil
}

const selectComplaint = `
	SELECT c.id::text, c.number, c.source, c.category, c.status, c.created_at, c.updated_at, c.closed_at,
	       octet_length(c.journal), COALESCE(c.account_id::text, ''), c.phone,
	       COALESCE(c.tenant_id::text, ''), COALESCE(t.name, ''),
	       COALESCE(c.closed_by::text, ''), COALESCE(u.email, '')
	FROM hub_complaints c
	LEFT JOIN tenants t ON t.id = c.tenant_id
	LEFT JOIN users u ON u.id = c.closed_by`

func scanComplaint(row pgx.Row) (Complaint, error) {
	var c Complaint
	var tenantName, closedBy, closedByEmail string
	err := row.Scan(&c.ID, &c.Number, &c.Source, &c.Category, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.ClosedAt,
		&c.JournalBytes, &c.AccountID, &c.Phone, &c.tenantID, &tenantName, &closedBy, &closedByEmail)
	if err != nil {
		return c, err
	}
	c.HasJournal = c.JournalBytes > 0
	if c.tenantID != "" {
		c.Household = &Household{TenantID: c.tenantID, Name: tenantName}
	}
	if closedBy != "" {
		c.ClosedBy = &Author{ID: closedBy, Email: closedByEmail}
	}
	return c, nil
}

// Find reads one complaint, its thread and (withJournal) its journal.
func (m *Module) Find(ctx context.Context, ref string, withJournal bool) (Complaint, error) {
	id, err := m.resolveRef(ctx, ref)
	if err != nil {
		return Complaint{}, err
	}
	c, err := scanComplaint(m.cfg.Pool.QueryRow(ctx, selectComplaint+` WHERE c.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if withJournal {
		var j string
		if err := m.cfg.Pool.QueryRow(ctx, `SELECT journal FROM hub_complaints WHERE id = $1`, id).Scan(&j); err != nil {
			return c, err
		}
		c.Journal = &j
	}
	threads, err := m.threads(ctx, []string{c.ID})
	if err != nil {
		return c, err
	}
	c.Messages = threads[c.ID]
	return c, nil
}

// threads reads the messages of some complaints, oldest first, with what
// each answer's WhatsApp copy became.
func (m *Module) threads(ctx context.Context, ids []string) (map[string][]Message, error) {
	out := map[string][]Message{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT cm.complaint_id::text, cm.id, cm.sender, cm.channel, cm.body, cm.created_at,
		       COALESCE(cm.author_id::text, ''), COALESCE(u.email, ''), cm.pushed, cm.push_failed, cm.whatsapp,
		       COALESCE((SELECT q.status FROM hub_whatsapp_queue q
		                 WHERE q.ref = 'complaint-message:' || cm.id ORDER BY q.id DESC LIMIT 1), '')
		FROM hub_complaint_messages cm
		LEFT JOIN users u ON u.id = cm.author_id
		WHERE cm.complaint_id = ANY($1::uuid[])
		ORDER BY cm.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, authorID, email, wa, queued string
		var msg Message
		var pushed, failed int
		if err := rows.Scan(&cid, &msg.ID, &msg.From, &msg.Channel, &msg.Body, &msg.At,
			&authorID, &email, &pushed, &failed, &wa, &queued); err != nil {
			return nil, err
		}
		if msg.From == FromAdmin {
			msg.Author = &Author{ID: authorID, Email: email}
			msg.Pushed, msg.PushFailed = &pushed, &failed
			msg.WhatsApp = wa
			if wa == WhatsAppQueued && queued != "" && queued != "queued" {
				msg.WhatsApp = queued // sent, failed
			}
		}
		out[cid] = append(out[cid], msg)
	}
	return out, rows.Err()
}

// Filter narrows the back-office's list.
type Filter struct {
	Status, Category, Source string
	// OlderThan keeps the complaints opened before now minus it.
	OlderThan time.Duration
	// Phone keeps one customer's complaints (E.164).
	Phone string
	Limit int
}

// List reads the complaints, newest first, without their journal.
func (m *Module) List(ctx context.Context, f Filter) ([]Complaint, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.Status != "" {
		add("c.status = $%d", f.Status)
	}
	if f.Category != "" {
		add("c.category = $%d", f.Category)
	}
	if f.Source != "" {
		add("c.source = $%d", f.Source)
	}
	if f.OlderThan > 0 {
		add("c.created_at < $%d", m.cfg.Now().UTC().Add(-f.OlderThan))
	}
	if f.Phone != "" {
		add("c.phone = $%d", f.Phone)
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	q := `
		SELECT c.id::text, c.number, c.source, c.category, c.status, c.created_at, c.updated_at, c.closed_at,
		       octet_length(c.journal), COALESCE(c.account_id::text, ''), c.phone,
		       COALESCE(c.tenant_id::text, ''), COALESCE(t.name, ''),
		       COALESCE((SELECT left(body, 140) FROM hub_complaint_messages WHERE complaint_id = c.id ORDER BY id LIMIT 1), ''),
		       (SELECT count(*) FROM hub_complaint_messages WHERE complaint_id = c.id),
		       COALESCE((SELECT sender FROM hub_complaint_messages WHERE complaint_id = c.id ORDER BY id DESC LIMIT 1), ''),
		       (SELECT max(created_at) FROM hub_complaint_messages WHERE complaint_id = c.id)
		FROM hub_complaints c LEFT JOIN tenants t ON t.id = c.tenant_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY c.created_at DESC, c.number DESC LIMIT $%d", len(args))
	rows, err := m.cfg.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Complaint{}
	for rows.Next() {
		var c Complaint
		var tenantName string
		if err := rows.Scan(&c.ID, &c.Number, &c.Source, &c.Category, &c.Status, &c.CreatedAt, &c.UpdatedAt, &c.ClosedAt,
			&c.JournalBytes, &c.AccountID, &c.Phone, &c.tenantID, &tenantName,
			&c.Preview, &c.MessageCount, &c.LastFrom, &c.LastMessageAt); err != nil {
			return nil, err
		}
		c.HasJournal = c.JournalBytes > 0
		if c.tenantID != "" {
			c.Household = &Household{TenantID: c.tenantID, Name: tenantName}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Counts is how many complaints stand where.
type Counts struct {
	Open     int `json:"open"`
	Answered int `json:"answered"`
	Closed   int `json:"closed"`
	// OpenOverDay are the open ones waiting for more than a day.
	OpenOverDay int `json:"openOverDay"`
}

// Count counts the complaints by status.
func (m *Module) Count(ctx context.Context) (Counts, error) {
	var c Counts
	err := m.cfg.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'open'),
		       count(*) FILTER (WHERE status = 'answered'),
		       count(*) FILTER (WHERE status = 'closed'),
		       count(*) FILTER (WHERE status = 'open' AND created_at < $1)
		FROM hub_complaints`, m.cfg.Now().UTC().Add(-24*time.Hour)).
		Scan(&c.Open, &c.Answered, &c.Closed, &c.OpenOverDay)
	return c, err
}

// --- answering --------------------------------------------------------------------

// Delivery is where an answer went.
type Delivery struct {
	// Pushed and PushFailed count the customer's phones.
	Pushed     int `json:"pushed"`
	PushFailed int `json:"pushFailed"`
	// WhatsApp is the WhatsApp copy: "" (not tried), queued,
	// not_opted_in, off.
	WhatsApp string `json:"whatsapp"`
}

// Answer is an admin's answer, stored and sent.
type Answer struct {
	Complaint Complaint `json:"complaint"`
	Message   Message   `json:"message"`
	Delivery  Delivery  `json:"delivery"`
}

// Answer stores an admin's answer, marks the complaint answered and sends
// it to the customer: push on every phone of the account (kind « reply »),
// and WhatsApp when the complaint came by WhatsApp or the customer opted
// in. authorID is the admin (the kernel user).
func (m *Module) Answer(ctx context.Context, ref, text, authorID string) (Answer, error) {
	return m.answerVia(ctx, ref, text, authorID, ChannelBackOffice)
}

// answerVia is Answer written from channel: the back-office, or WhatsApp for
// an admin's « R n texte » (issue #83), whose author has no kernel user.
func (m *Module) answerVia(ctx context.Context, ref, text, authorID, channel string) (Answer, error) {
	var out Answer
	body, err := cleanBody(text)
	if err != nil {
		return out, err
	}
	id, err := m.resolveRef(ctx, ref)
	if err != nil {
		return out, err
	}
	now := m.cfg.Now().UTC()
	tx, err := m.cfg.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM hub_complaints WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	if status == StatusClosed {
		return out, ErrClosed
	}
	var author *string
	if authorID != "" {
		author = &authorID
	}
	msg := Message{From: FromAdmin, Channel: channel, Body: body, At: now}
	if err := tx.QueryRow(ctx, `
		INSERT INTO hub_complaint_messages (complaint_id, sender, channel, body, author_id, created_at)
		VALUES ($1, 'admin', $2, $3, $4, $5) RETURNING id`, id, msg.Channel, body, author, now).Scan(&msg.ID); err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `UPDATE hub_complaints SET status = 'answered', updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}

	c, err := m.Find(ctx, id, false)
	if err != nil {
		return out, err
	}
	// The sends outlive the admin's request.
	sendCtx := context.WithoutCancel(ctx)
	d := m.deliver(sendCtx, c, msg)
	if _, err := m.cfg.Pool.Exec(sendCtx, `
		UPDATE hub_complaint_messages SET pushed = $2, push_failed = $3, whatsapp = $4 WHERE id = $1`,
		msg.ID, d.Pushed, d.PushFailed, d.WhatsApp); err != nil && m.cfg.Logger != nil {
		m.cfg.Logger.Warn("complaints: cannot record an answer's delivery", "number", c.Number, "error", err)
	}
	if c, err = m.Find(sendCtx, id, false); err != nil {
		return out, err
	}
	for _, mm := range c.Messages {
		if mm.ID == msg.ID {
			msg = mm
		}
	}
	return Answer{Complaint: c, Message: msg, Delivery: d}, nil
}

// replyTitle is the answer's notification title.
func replyTitle(number int64) string {
	return fmt.Sprintf("Réponse à votre demande n° %d", number)
}

// truncate cuts s to n characters, with an ellipsis.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// pushBodyMax keeps the push under the providers' 4 KB: the app reads the
// whole thread from the complaints route.
const pushBodyMax = 1000

// deliver sends an answer to the customer's phones and WhatsApp.
func (m *Module) deliver(ctx context.Context, c Complaint, msg Message) Delivery {
	var d Delivery
	if m.cfg.Pusher != nil {
		targets, err := m.targets(ctx, c)
		if err != nil && m.cfg.Logger != nil {
			m.cfg.Logger.Warn("complaints: cannot list the customer's phones", "number", c.Number, "error", err)
		}
		title := replyTitle(c.Number)
		for _, t := range targets {
			tenant := t.TenantID
			if c.tenantID != "" {
				tenant = c.tenantID
			}
			err := m.cfg.Pusher.Push(ctx, t, alertsdomain.PushMessage{
				Title: title, Body: truncate(msg.Body, 180),
				Data: map[string]string{
					"kind": PushKind, "complaintId": c.ID, "number": strconv.FormatInt(c.Number, 10),
					"category": c.Category, "status": StatusAnswered, "title": title,
					"body": truncate(msg.Body, pushBodyMax), "messageId": strconv.FormatInt(msg.ID, 10),
					"tenantId": tenant,
				},
			})
			if err == nil {
				d.Pushed++
				continue
			}
			d.PushFailed++
			if errors.Is(err, alertsdomain.ErrPushTokenGone) && m.cfg.Targets != nil {
				if ferr := m.cfg.Targets.Forget(ctx, t.Token); ferr != nil && m.cfg.Logger != nil {
					m.cfg.Logger.Warn("complaints: cannot forget a gone push token", "error", ferr)
				}
			} else if m.cfg.Logger != nil {
				m.cfg.Logger.Warn("complaints: push failed", "provider", m.cfg.Pusher.Name(),
					"user_id", t.UserID, "platform", t.Platform, "error", err)
			}
		}
	}
	d.WhatsApp = m.whatsApp(ctx, c, msg)
	return d
}

// targets are the phones of the customer's account, one per token: the
// installations registered by the account's hub user, in any household.
func (m *Module) targets(ctx context.Context, c Complaint) ([]alertsdomain.PushTarget, error) {
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT p.tenant_id::text, p.user_id::text, p.platform, p.token
		FROM hub_push_tokens p
		JOIN hub_identity_households h ON h.user_id = p.user_id
		JOIN hub_licensing_accounts a ON a.id = h.account_id
		WHERE a.id = NULLIF($1, '')::uuid OR a.phone = $2
		ORDER BY p.created_at`, c.AccountID, c.Phone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []alertsdomain.PushTarget
	seen := map[string]bool{}
	for rows.Next() {
		var t alertsdomain.PushTarget
		if err := rows.Scan(&t.TenantID, &t.UserID, &t.Platform, &t.Token); err != nil {
			return nil, err
		}
		if seen[t.Token] {
			continue
		}
		seen[t.Token] = true
		out = append(out, t)
	}
	return out, rows.Err()
}

// replyText is the answer's WhatsApp text.
func replyText(number int64, body string) string {
	return fmt.Sprintf("*Kergui — réponse à votre demande n° %d*\n\n%s\n\n— Kergui · Répondez STOP pour ne plus rien recevoir de Kergui sur WhatsApp.", number, body)
}

// whatsApp queues the answer's WhatsApp copy, when it has somewhere to go.
func (m *Module) whatsApp(ctx context.Context, c Complaint, msg Message) string {
	if m.cfg.WhatsApp == nil {
		return WhatsAppOff
	}
	d := messages.Direct{
		Phone: alertsdomain.Phone(c.Phone), Text: replyText(c.Number, msg.Body),
		Ref: fmt.Sprintf("complaint-message:%d", msg.ID),
	}
	if c.Source == SourceWhatsApp {
		// The customer wrote to Kergui: the answer goes back the same way.
		d.TenantID = c.tenantID
	} else {
		var tenant, user string
		err := m.cfg.Pool.QueryRow(ctx, `
			SELECT tenant_id::text, user_id::text FROM hub_whatsapp_optins
			WHERE phone = $1 AND stopped_at IS NULL ORDER BY created_at LIMIT 1`, c.Phone).Scan(&tenant, &user)
		if errors.Is(err, pgx.ErrNoRows) {
			return WhatsAppNotOptedIn
		}
		if err != nil {
			if m.cfg.Logger != nil {
				m.cfg.Logger.Warn("complaints: cannot read the opt-in", "number", c.Number, "error", err)
			}
			return ""
		}
		d.TenantID, d.UserID, d.OptInRequired = tenant, user, true
	}
	if err := m.cfg.WhatsApp.EnqueueWhatsApp(ctx, d); err != nil {
		if errors.Is(err, messages.ErrWhatsAppOff) {
			return WhatsAppOff
		}
		if m.cfg.Logger != nil {
			m.cfg.Logger.Warn("complaints: cannot queue the WhatsApp answer", "number", c.Number, "error", err)
		}
		return ""
	}
	return WhatsAppQueued
}

// Close closes a complaint. authorID is the admin.
func (m *Module) Close(ctx context.Context, ref, authorID string) (Complaint, error) {
	id, err := m.resolveRef(ctx, ref)
	if err != nil {
		return Complaint{}, err
	}
	var author *string
	if authorID != "" {
		author = &authorID
	}
	now := m.cfg.Now().UTC()
	tag, err := m.cfg.Pool.Exec(ctx, `
		UPDATE hub_complaints SET status = 'closed', closed_at = $2, closed_by = $3, updated_at = $2
		WHERE id = $1 AND status <> 'closed'`, id, now, author)
	if err != nil {
		return Complaint{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := m.Find(ctx, id, false); err != nil {
			return Complaint{}, err
		}
		return Complaint{}, ErrClosed
	}
	return m.Find(ctx, id, false)
}

// record writes an admin gesture on a complaint to the action log.
func (m *Module) record(ctx context.Context, kind actionlogKind, c Complaint, summary string, details map[string]any) {
	actionlog.Record(ctx, m.cfg.Audit, actionlog.Gesture{
		Kind: kind, TenantID: c.tenantID, TargetType: actionlog.TargetComplaint, TargetID: c.ID,
		Summary: summary, Details: details,
	})
}
