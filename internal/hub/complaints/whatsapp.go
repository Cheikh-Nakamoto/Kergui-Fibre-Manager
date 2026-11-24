package complaints

// Complaints over WhatsApp (issue #83, parent #68).
//
// The alerts module reads every message sent to the Kergui number first:
// alert replies (« 1 », « OK »), STOP and the opt-in keep their meaning.
// What it does not understand comes here, through Receive:
//
//   - from an admin's number (the complaint_admins setting), a command:
//     LISTE, VOIR <n>, R <n> <texte>, OK <n>, anything else gets the help;
//   - from anyone else, a complaint: a new one, or one more message on the
//     sender's most recent open or answered complaint, acknowledged.
//
// The admins never get the complaints themselves on WhatsApp, only a
// summary (« 3 nouvelles plaintes (2 paiement, 1 box)… ») at most once per
// SummaryInterval, sent by Run. Everything here leaves through the
// back-office's WhatsApp queue and its pace (#81): the acknowledgement, the
// commands' answers and the summaries alike.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/messages"
)

// What the WhatsApp side shows at most.
const (
	// ListMax is how many complaints LISTE shows; the rest are counted.
	ListMax = 15
	// listPreview and detailBody cut a message in LISTE and in VOIR.
	listPreview = 60
	detailBody  = 300
	// detailMessages is how many of the last messages VOIR shows.
	detailMessages = 5
	// ackQuiet: a message added to a complaint is acknowledged again only
	// if the customer's previous one is older than this, so five lines
	// typed in a row get one acknowledgement, not five.
	ackQuiet = 15 * time.Minute
	// overDay is when an open complaint counts as waiting too long.
	overDay = 24 * time.Hour
)

// The commands' verbs.
const (
	VerbList  = "LISTE"
	VerbView  = "VOIR"
	VerbReply = "R"
	VerbClose = "OK"
)

// HelpText answers an admin's message that is no command.
const HelpText = "Commandes plaintes :\n" +
	"LISTE — les plaintes ouvertes\n" +
	"VOIR 12 — le détail de la plainte n° 12\n" +
	"R 12 votre texte — répondre au client\n" +
	"OK 12 — clore la plainte\n" +
	"Tout est aussi dans le back-office, page « Plaintes »."

// Receive takes a WhatsApp message the alerts' grammar did not match: an
// admin's command, or a customer's complaint. It answers through the
// WhatsApp queue and never fails: what cannot be done is logged.
func (m *Module) Receive(ctx context.Context, msg alertsdomain.InboundMessage) {
	text := strings.TrimSpace(cleanText(msg.Text))
	if text == "" {
		// The providers drop media and empty messages before this; a
		// message with nothing to read makes no complaint either.
		return
	}
	isAdmin, err := m.IsAdminNumber(ctx, msg.From)
	if err != nil {
		m.warn("complaints: cannot read the admins' numbers", "error", err)
		return
	}
	if isAdmin {
		m.say(ctx, msg.From, m.Command(ctx, msg.From, text), "complaint-command")
		return
	}
	m.intakeWhatsApp(ctx, msg.From, text)
}

// intakeWhatsApp makes a customer's message a complaint, or adds it to the
// sender's open or answered complaint, and acknowledges it.
func (m *Module) intakeWhatsApp(ctx context.Context, from alertsdomain.Phone, text string) {
	m.intake.Lock()
	defer m.intake.Unlock()
	if utf8Len(text) > MaxMessage {
		text = truncate(text, MaxMessage)
	}

	var (
		id, status   string
		number       int64
		lastCustomer *time.Time
	)
	err := m.cfg.Pool.QueryRow(ctx, `
		SELECT c.id::text, c.number, c.status,
		       (SELECT max(created_at) FROM hub_complaint_messages WHERE complaint_id = c.id AND sender = 'customer')
		FROM hub_complaints c
		WHERE c.phone = $1 AND c.status IN ('open', 'answered')
		ORDER BY c.updated_at DESC LIMIT 1`, string(from)).Scan(&id, &number, &status, &lastCustomer)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if isPleasantry(text) {
			// « Merci » after an alert is no complaint.
			return
		}
		c, err := m.Open(ctx, Intake{Source: SourceWhatsApp, Category: GuessCategory(text), Phone: string(from), Body: text})
		if err != nil {
			m.warn("complaints: cannot open a WhatsApp complaint", "from", from.Masked(), "error", err)
			return
		}
		m.say(ctx, from, AckNew(c.Number), fmt.Sprintf("complaint:%d:ack", c.Number))
	case err != nil:
		m.warn("complaints: cannot find the sender's complaint", "from", from.Masked(), "error", err)
	default:
		if _, err := m.AddCustomerMessage(ctx, id, SourceWhatsApp, text); err != nil {
			m.warn("complaints: cannot add a WhatsApp message", "number", number, "from", from.Masked(), "error", err)
			return
		}
		quiet := lastCustomer == nil || m.cfg.Now().Sub(*lastCustomer) >= ackQuiet
		if status == StatusAnswered || quiet {
			m.say(ctx, from, AckAdded(number), fmt.Sprintf("complaint:%d:ack", number))
		}
	}
}

// AckNew acknowledges a new WhatsApp complaint.
func AckNew(number int64) string {
	return fmt.Sprintf("Plainte n° %d reçue. L'équipe Kergui vous répondra ici dès que possible. "+
		"Écrivez à nouveau pour ajouter des précisions.", number)
}

// AckAdded acknowledges a message added to a complaint.
func AckAdded(number int64) string {
	return fmt.Sprintf("Message ajouté à votre plainte n° %d. L'équipe Kergui vous répondra ici.", number)
}

// say queues a WhatsApp text to a number that wrote to Kergui first.
func (m *Module) say(ctx context.Context, to alertsdomain.Phone, text, ref string) {
	if m.cfg.WhatsApp == nil {
		return
	}
	err := m.cfg.WhatsApp.EnqueueWhatsApp(context.WithoutCancel(ctx), messages.Direct{Phone: to, Text: text, Ref: ref})
	if err != nil {
		m.warn("complaints: cannot queue a WhatsApp text", "to", to.Masked(), "ref", ref, "error", err)
	}
}

func (m *Module) warn(msg string, args ...any) {
	if m.cfg.Logger != nil {
		m.cfg.Logger.Warn(msg, args...)
	}
}

// --- the category -------------------------------------------------------------------

// categoryWords say what a WhatsApp complaint is about: whole words, and
// starts of words; the first category with a match wins.
var categoryWords = []struct {
	category string
	words    []string
	prefixes []string
}{
	{CategoryPayment, []string{"pass", "pro", "om"},
		[]string{"paie", "pay", "wave", "rembours", "abonn", "factur", "fcfa", "argent", "débit", "prélev", "prelev"}},
	{CategoryBox, []string{"box", "wifi", "lent"},
		[]string{"routeur", "router", "internet", "connexion", "connect", "réseau", "reseau", "fibre", "coupure", "coupé"}},
	{CategoryApp, []string{"app", "bug"},
		[]string{"appli", "plante", "écran", "ecran", "notif"}},
}

// GuessCategory reads a WhatsApp complaint's category from its words: a
// WhatsApp message has no form, and the summary counts by category.
func GuessCategory(text string) string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	for _, c := range categoryWords {
		for _, w := range words {
			if slices.Contains(c.words, w) {
				return c.category
			}
			for _, p := range c.prefixes {
				if strings.HasPrefix(w, p) {
					return c.category
				}
			}
		}
	}
	return CategoryOther
}

// pleasantries are what a customer says that is not a problem.
var pleasantries = []string{
	"merci", "merci beaucoup", "ok merci", "merci ok", "d accord", "dac", "bien reçu", "bien recu", "super", "parfait",
	"bonne journée", "bonne journee", "bonne soirée", "bonne soiree", "jerejef", "jërëjëf",
}

// isPleasantry reports whether text is only thanks or an emoji.
func isPleasantry(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	if len(words) == 0 {
		return true // emojis, punctuation
	}
	return slices.Contains(pleasantries, strings.Join(words, " "))
}

// --- the admins' commands ------------------------------------------------------------

// Cmd is an admin's command as read.
type Cmd struct {
	Verb string
	// Number is the complaint's number; 0 when it is missing.
	Number int64
	Text   string
}

// ParseCommand reads an admin's message: a verb (LISTE, VOIR, R, OK, any
// case), then for the last three a number (« 12 » or « #12 », spaces
// tolerated), then for R the text. ok is false for anything else.
func ParseCommand(s string) (Cmd, bool) {
	s = strings.TrimSpace(s)
	end := strings.IndexFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
	if end < 0 {
		end = len(s)
	}
	verb := strings.ToUpper(s[:end])
	switch verb {
	case VerbList, VerbView, VerbReply, VerbClose:
	default:
		return Cmd{}, false
	}
	c := Cmd{Verb: verb}
	rest := strings.TrimSpace(s[end:])
	if verb == VerbList {
		return c, rest == ""
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "#"))
	digits := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if digits < 0 {
		digits = len(rest)
	}
	if digits > 0 {
		n, err := strconv.ParseInt(rest[:digits], 10, 64)
		if err != nil {
			return Cmd{}, false
		}
		c.Number = n
	}
	c.Text = strings.TrimSpace(rest[digits:])
	switch verb {
	case VerbReply:
		// « R 12 » without a text, or « R bonjour », is not a command.
		return c, c.Number > 0 && c.Text != ""
	default:
		return c, c.Number > 0 && c.Text == ""
	}
}

// Command runs an admin's command and returns the answer to send back.
func (m *Module) Command(ctx context.Context, from alertsdomain.Phone, text string) string {
	cmd, ok := ParseCommand(text)
	if !ok {
		return HelpText
	}
	ref := strconv.FormatInt(cmd.Number, 10)
	switch cmd.Verb {
	case VerbList:
		return m.listText(ctx)
	case VerbView:
		return m.detailText(ctx, cmd.Number)
	case VerbReply:
		a, err := m.answerVia(ctx, ref, cmd.Text, "", ChannelWhatsApp)
		switch {
		case errors.Is(err, ErrNotFound):
			return fmt.Sprintf("Pas de plainte n° %d.", cmd.Number)
		case errors.Is(err, ErrClosed):
			return fmt.Sprintf("La plainte n° %d est close : la réponse n'a pas été envoyée.", cmd.Number)
		case errors.Is(err, ErrInvalid):
			return fmt.Sprintf("Réponse trop longue : %d caractères au plus.", MaxMessage)
		case err != nil:
			m.warn("complaints: a WhatsApp answer failed", "number", cmd.Number, "error", err)
			return "La réponse n'a pas pu être enregistrée. Réessayez, ou passez par le back-office."
		}
		wa := map[string]string{
			WhatsAppQueued: "WhatsApp en file", WhatsAppNotOptedIn: "pas de WhatsApp (non activé)", WhatsAppOff: "WhatsApp non configuré",
		}[a.Delivery.WhatsApp]
		if wa == "" {
			wa = "pas de WhatsApp"
		}
		m.record(ctx, actionlog.KindComplaintAnswered, a.Complaint,
			fmt.Sprintf("Réponse à la plainte n° %d (%s, %s) par WhatsApp depuis %s : %d téléphone(s), %s",
				a.Complaint.Number, a.Complaint.Category, a.Complaint.Phone, from, a.Delivery.Pushed, wa),
			map[string]any{"number": a.Complaint.Number, "category": a.Complaint.Category, "source": a.Complaint.Source,
				"messageId": a.Message.ID, "pushed": a.Delivery.Pushed, "pushFailed": a.Delivery.PushFailed,
				"whatsapp": a.Delivery.WhatsApp, "channel": ChannelWhatsApp, "adminPhone": string(from)})
		return fmt.Sprintf("✅ Réponse envoyée au client de la plainte n° %d (%s, %d téléphone(s) notifié(s)).",
			a.Complaint.Number, wa, a.Delivery.Pushed)
	default: // VerbClose
		c, err := m.Close(ctx, ref, "")
		switch {
		case errors.Is(err, ErrNotFound):
			return fmt.Sprintf("Pas de plainte n° %d.", cmd.Number)
		case errors.Is(err, ErrClosed):
			return fmt.Sprintf("La plainte n° %d est déjà close.", cmd.Number)
		case err != nil:
			m.warn("complaints: a WhatsApp close failed", "number", cmd.Number, "error", err)
			return "La plainte n'a pas pu être close. Réessayez, ou passez par le back-office."
		}
		m.record(ctx, actionlog.KindComplaintClosed, c,
			fmt.Sprintf("Plainte n° %d close (%s, %s) par WhatsApp depuis %s", c.Number, c.Category, c.Phone, from),
			map[string]any{"number": c.Number, "category": c.Category, "source": c.Source,
				"channel": ChannelWhatsApp, "adminPhone": string(from)})
		return fmt.Sprintf("✅ Plainte n° %d close.", c.Number)
	}
}

// ChannelWhatsApp is an admin's answer written on WhatsApp (« R 12 … »).
const ChannelWhatsApp = SourceWhatsApp

var statusLabel = map[string]string{StatusOpen: "ouverte", StatusAnswered: "répondue", StatusClosed: "close"}
var sourceLabel = map[string]string{SourceApp: "application", SourceWeb: "site", SourceWhatsApp: "WhatsApp"}

// oneLine flattens a message for a list line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// stamp is a time as the admins read it; Dakar is UTC all year.
func stamp(t time.Time) string { return t.UTC().Format("02/01 15:04") }

// listText is LISTE: the open complaints, the longest waiting first.
func (m *Module) listText(ctx context.Context) string {
	open, err := m.List(ctx, Filter{Status: StatusOpen, Limit: 200})
	if err != nil {
		m.warn("complaints: LISTE", "error", err)
		return "La liste n'a pas pu être lue. Réessayez, ou passez par le back-office."
	}
	if len(open) == 0 {
		return "Aucune plainte ouverte."
	}
	slices.Reverse(open) // List is newest first
	var b strings.Builder
	fmt.Fprintf(&b, "Plaintes ouvertes : %d\n", len(open))
	now := m.cfg.Now()
	for _, c := range open[:min(len(open), ListMax)] {
		fmt.Fprintf(&b, "\nn° %d · %s · %s", c.Number, alertsdomain.Phone(c.Phone).Masked(), c.Category)
		if days := int(now.Sub(c.CreatedAt) / overDay); days >= 1 {
			fmt.Fprintf(&b, " · depuis %d j", days)
		}
		fmt.Fprintf(&b, "\n%s\n", truncate(oneLine(c.Preview), listPreview))
	}
	if more := len(open) - ListMax; more > 0 {
		fmt.Fprintf(&b, "\n… et %d autre(s) dans le back-office.\n", more)
	}
	b.WriteString("\nVOIR n pour le détail, R n texte pour répondre, OK n pour clore.")
	return b.String()
}

// detailText is VOIR n: the customer, their boxes and passes, the last
// messages.
func (m *Module) detailText(ctx context.Context, number int64) string {
	c, err := m.Find(ctx, strconv.FormatInt(number, 10), false)
	if errors.Is(err, ErrNotFound) {
		return fmt.Sprintf("Pas de plainte n° %d.", number)
	}
	if err != nil {
		m.warn("complaints: VOIR", "number", number, "error", err)
		return "La plainte n'a pas pu être lue. Réessayez, ou passez par le back-office."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "*Plainte n° %d* · %s · %s · %s\n", c.Number, statusLabel[c.Status], c.Category, sourceLabel[c.Source])
	fmt.Fprintf(&b, "Client : %s\n", c.Phone)
	if c.AccountID == "" {
		b.WriteString("Pas de compte Kergui à ce numéro.\n")
	} else {
		if c.Household != nil {
			fmt.Fprintf(&b, "Foyer : %s\n", c.Household.Name)
		}
		passes, err := m.passes(ctx, c.AccountID)
		if err != nil {
			m.warn("complaints: VOIR passes", "number", number, "error", err)
		}
		if len(passes) == 0 {
			b.WriteString("Box : aucune\n")
		}
		for _, p := range passes {
			fmt.Fprintf(&b, "Box %s\n", p)
		}
	}
	fmt.Fprintf(&b, "Reçue le %s", stamp(c.CreatedAt))
	if c.ClosedAt != nil {
		fmt.Fprintf(&b, ", close le %s", stamp(*c.ClosedAt))
	}
	b.WriteString("\n")
	msgs := c.Messages
	if len(msgs) > detailMessages {
		fmt.Fprintf(&b, "\n%d derniers messages sur %d :", detailMessages, len(msgs))
		msgs = msgs[len(msgs)-detailMessages:]
	}
	for _, msg := range msgs {
		who := "Client"
		if msg.From == FromAdmin {
			who = "Kergui"
		}
		fmt.Fprintf(&b, "\n[%s] %s : %s\n", stamp(msg.At), who, truncate(msg.Body, detailBody))
	}
	if c.Status != StatusClosed {
		fmt.Fprintf(&b, "\nR %d votre texte pour répondre, OK %d pour clore.", c.Number, c.Number)
	}
	return b.String()
}

// passes are an account's boxes, each with its level and end date.
func (m *Module) passes(ctx context.Context, accountID string) ([]string, error) {
	rows, err := m.cfg.Pool.Query(ctx, `
		SELECT box_id, tier, expires_at FROM hub_licensing_subscriptions
		WHERE account_id = $1::uuid ORDER BY expires_at DESC LIMIT 5`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := m.cfg.Now()
	var out []string
	for rows.Next() {
		var box, tier string
		var until time.Time
		if err := rows.Scan(&box, &tier, &until); err != nil {
			return nil, err
		}
		level := map[string]string{"free": "gratuit", "basic": "Basic", "max": "Max"}[tier]
		if level == "" {
			level = tier
		}
		switch {
		case tier == "free":
			out = append(out, fmt.Sprintf("%s : %s", box, level))
		case until.After(now):
			out = append(out, fmt.Sprintf("%s : %s jusqu'au %s", box, level, until.UTC().Format("02/01/2006")))
		default:
			out = append(out, fmt.Sprintf("%s : %s, expiré le %s", box, level, until.UTC().Format("02/01/2006")))
		}
	}
	return out, rows.Err()
}

// --- the admins' summary -------------------------------------------------------------

// Run sends the admins' summary until ctx ends: it looks a few times per
// interval, and Summarize decides.
func (m *Module) Run(ctx context.Context) error {
	tick := min(max(m.cfg.SummaryInterval/10, 10*time.Millisecond), time.Minute)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if _, err := m.Summarize(ctx); err != nil && ctx.Err() == nil {
				m.warn("complaints: the admins' summary", "error", err)
			}
		}
	}
}

// Summary is what one summary counted.
type Summary struct {
	New        int
	ByCategory map[string]int
	OverDay    int
	Text       string
	Admins     int
}

// Summarize sends the admins' summary if one is due: complaints came in
// since the last one, and the last one is at least SummaryInterval old.
// The complaints it counts are claimed (summarized_at) in the transaction
// that moves the summary's clock, under that setting's row lock: two hubs
// at once send one summary, and a complaint is never counted twice. With no
// admin number nothing is claimed, so the first admin set hears of them.
func (m *Module) Summarize(ctx context.Context) (Summary, error) {
	var s Summary
	admins, err := m.AdminNumbers(ctx)
	if err != nil || len(admins) == 0 {
		return s, err
	}
	now := m.cfg.Now().UTC()
	tx, err := m.cfg.Pool.Begin(ctx)
	if err != nil {
		return s, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO hub_settings (key, value, updated_at) VALUES ($1, '{}'::jsonb, $2) ON CONFLICT (key) DO NOTHING`,
		SettingSummary, now); err != nil {
		return s, err
	}
	var last *time.Time
	err = tx.QueryRow(ctx, `SELECT (value->>'at')::timestamptz FROM hub_settings WHERE key = $1 FOR UPDATE SKIP LOCKED`,
		SettingSummary).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil // another hub is summarizing
	}
	if err != nil {
		return s, err
	}
	if last != nil && now.Before(last.Add(m.cfg.SummaryInterval)) {
		return s, nil
	}
	rows, err := tx.Query(ctx, `
		UPDATE hub_complaints SET summarized_at = $1 WHERE summarized_at IS NULL RETURNING category`, now)
	if err != nil {
		return s, err
	}
	s.ByCategory = map[string]int{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return s, err
		}
		s.ByCategory[c]++
		s.New++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}
	if s.New == 0 {
		return s, nil
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM hub_complaints WHERE status = 'open' AND created_at < $1`,
		now.Add(-overDay)).Scan(&s.OverDay); err != nil {
		return s, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE hub_settings SET value = jsonb_build_object('at', $2::timestamptz), updated_at = $2 WHERE key = $1`,
		SettingSummary, now); err != nil {
		return s, err
	}
	if err := tx.Commit(ctx); err != nil {
		return s, err
	}
	s.Text = SummaryText(s.New, s.ByCategory, s.OverDay)
	for _, a := range admins {
		phone, err := alertsdomain.ParsePhone(a)
		if err != nil {
			continue
		}
		m.say(ctx, phone, s.Text, "complaint-summary")
		s.Admins++
	}
	return s, nil
}

// SummaryText is the summary: « 3 nouvelles plaintes (2 paiement, 1 box),
// dont 1 en attente depuis plus d'un jour. Répondez LISTE pour les voir. »
// overDay counts every open complaint waiting for more than a day, so an
// old one is recalled with each summary until someone answers it.
func SummaryText(total int, byCategory map[string]int, overDay int) string {
	var parts []string
	for _, c := range []string{CategoryPayment, CategoryBox, CategoryApp, CategoryOther} {
		if n := byCategory[c]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c))
		}
	}
	var b strings.Builder
	if total == 1 {
		b.WriteString("1 nouvelle plainte")
	} else {
		fmt.Fprintf(&b, "%d nouvelles plaintes", total)
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}
	if overDay > 0 {
		fmt.Fprintf(&b, ", dont %d en attente depuis plus d'un jour", overDay)
	}
	if total == 1 {
		b.WriteString(". Répondez LISTE pour la voir.")
	} else {
		b.WriteString(". Répondez LISTE pour les voir.")
	}
	return b.String()
}

func utf8Len(s string) int { return len([]rune(s)) }
