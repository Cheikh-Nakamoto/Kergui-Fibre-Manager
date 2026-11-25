package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
)

// Renewal reminders (issue #99, extending RFC-006's J-3 WhatsApp reminder).
// For each end date of a prepaid pass, the trial included, two reminders go
// out: three days before (J-3) and on the day (J0), each once, during the
// day in Dakar, by two channels:
//
//   - WhatsApp, with the link to renew (RenewURL, else the hub's purchase
//     page for that box): outside the app, so it may say where to pay;
//   - the app: a push and an entry in its notifications (the messages
//     module's announcements), which say when the pass ends and nothing
//     about paying: RFC-006 forbids the app any text or link that points to
//     a website to pay (Google Play's steering rule).
//
// A pass renewed has a new end date, so the reminders of the old one are no
// longer due. Not reminded either, and logged as skipped for the
// back-office: a trial whose number already paid for a pass (converted),
// and an account holding a paid pass that waits for its box.

// ReminderStatus is what one channel of a reminder did.
type ReminderStatus string

const (
	// ReminderSending is a claimed reminder whose channel has not answered
	// yet. A hub stopped right then leaves it so, and it is not sent again:
	// a reminder missed is better than one sent twice.
	ReminderSending ReminderStatus = "sending"
	ReminderSent    ReminderStatus = "sent"
	// ReminderFailed is a channel that refused: a WhatsApp message is tried
	// again on the next sweeps, domain.ReminderTries times in all.
	ReminderFailed ReminderStatus = "failed"
	// ReminderNone is an app notification with no phone to go to: nobody of
	// the household signed in to the app.
	ReminderNone ReminderStatus = "none"
	// ReminderOff is an app notification on a hub without push.
	ReminderOff ReminderStatus = "off"
	// ReminderSkipped is a reminder not sent on purpose (its Note says why).
	ReminderSkipped ReminderStatus = "skipped"
)

// Why a reminder was skipped, as the back-office shows it.
const (
	NoteTrialConverted = "Essai déjà converti : le numéro a payé un pass."
	NoteWaitingPass    = "Un pass payé attend sa box : il s'ajoutera à la prochaine connexion de l'application."
)

// ReminderQuery is the window of end dates a sweep looks at. A pass ending
// in [DayStart, DayEnd) (today in Dakar) is due its J0 reminder; one ending
// after DayEnd and at or before Until (ReminderLead from now) its J-3 one.
type ReminderQuery struct {
	DayStart, DayEnd, Until time.Time
	// Tries is how many WhatsApp tries a failed reminder gets.
	Tries int
}

// DueReminder is one reminder to send: the step of the subscription's
// current end date.
type DueReminder struct {
	Sub  domain.Subscription
	Step domain.ReminderStep
	// Retry is a reminder already claimed whose WhatsApp message failed:
	// only WhatsApp is tried again, the app notification went out already.
	Retry bool
}

// ReminderOutcome is what a claimed reminder did, per channel.
type ReminderOutcome struct {
	WhatsApp ReminderStatus
	// Push is empty on a retry: the store keeps what the first try did.
	Push       ReminderStatus
	PushPhones int
	PushSent   int
	Note       string
}

// AppNotice is what an app notification reached: the household's phones,
// and how many took it.
type AppNotice struct{ Phones, Sent int }

// ErrNoticesOff is an app notification asked of a hub without push.
var ErrNoticesOff = errors.New("licensing: app notifications are not configured on this hub")

// AppNotifier pushes a notification to the phones of the account's
// household and files it in the app's notifications. An account with no
// household yet (never signed in to the app) reaches no phone, which is no
// error.
type AppNotifier interface {
	NotifyAccount(ctx context.Context, a domain.AccountID, title, body string) (AppNotice, error)
}

// reminderBatch is how many reminders one sweep sends at most; the next
// sweep, ten minutes later, sends the rest.
const reminderBatch = 100

// SendReminders sends the renewal reminders due now, and returns how many
// reached the person by at least one channel. Outside the day's hours in
// Dakar, or with the back-office's switch off, it sends nothing: the
// reminders still due when it is back on go out then. A reminder is claimed
// before it is sent, so two hub instances do not both send it.
func (s *Service) SendReminders(ctx context.Context) (int, error) {
	now := s.now()
	if !domain.ReminderHours(now) {
		return 0, nil
	}
	if s.RemindersOn != nil {
		on, err := s.RemindersOn(ctx)
		if err != nil {
			return 0, fmt.Errorf("licensing: reminders setting: %w", err)
		}
		if !on {
			return 0, nil
		}
	}
	day := domain.DayStart(now)
	due, err := s.Store.DueReminders(ctx, ReminderQuery{
		DayStart: day, DayEnd: day.Add(24 * time.Hour), Until: now.Add(domain.ReminderLead), Tries: domain.ReminderTries,
	}, reminderBatch)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, d := range due {
		// The store's window and the domain's rule must agree; the domain
		// has the last word.
		if step, ok := d.Sub.ReminderStepAt(now); !ok || step != d.Step {
			continue
		}
		reached, err := s.remind(ctx, d, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("box %s: %w", d.Sub.BoxID, err))
		}
		if reached {
			n++
		}
	}
	return n, errors.Join(errs...)
}

func (s *Service) remind(ctx context.Context, d DueReminder, now time.Time) (bool, error) {
	acc, err := s.Store.AccountByID(ctx, d.Sub.Account)
	if err != nil {
		return false, err
	}
	r, note, err := s.reminderFor(ctx, d, now)
	if err != nil {
		return false, err
	}
	ok, err := s.Store.ClaimReminder(ctx, d, now)
	if err != nil || !ok {
		return false, err
	}
	out := ReminderOutcome{WhatsApp: ReminderSent}
	if note != "" {
		out = ReminderOutcome{WhatsApp: ReminderSkipped, Push: ReminderSkipped, Note: note}
		if d.Retry {
			out.Push = ""
		}
		return false, s.Store.RecordReminder(ctx, d, out)
	}
	var errs []error
	if _, err := s.Sender.Send(ctx, acc.Phone, alerts.OutboundMessage{Text: r.whatsApp(s.renewLink(d.Sub))}); err != nil {
		out.WhatsApp = ReminderFailed
		errs = append(errs, fmt.Errorf("whatsapp: %w", err))
	}
	if !d.Retry {
		out.Push, out.PushPhones, out.PushSent = s.notifyApp(ctx, d.Sub.Account, r)
	}
	if err := s.Store.RecordReminder(ctx, d, out); err != nil {
		errs = append(errs, err)
	}
	// The account and the box say who; the number stays out of the logs.
	s.info("licensing: renewal reminder", "account", d.Sub.Account, "box", d.Sub.BoxID, "step", d.Step,
		"expires_at", d.Sub.ExpiresAt, "whatsapp", out.WhatsApp, "push", out.Push, "retry", d.Retry)
	return out.WhatsApp == ReminderSent || out.Push == ReminderSent, errors.Join(errs...)
}

// reminderFor builds the reminder of d, or says why it is skipped: a trial
// whose number already paid for a pass, or a paid pass waiting for its box.
func (s *Service) reminderFor(ctx context.Context, d DueReminder, now time.Time) (reminder, string, error) {
	r := reminder{sub: d.Sub, step: d.Step, ended: !d.Sub.Active(now)}
	t, err := s.Store.AccountTrial(ctx, d.Sub.Account)
	switch {
	case err == nil:
		r.trial = t.BoxID == d.Sub.BoxID && d.Sub.TrialEnd(t.StartedAt)
	case !errors.Is(err, ErrNotFound):
		return r, "", err
	}
	if r.trial {
		pays, err := s.Store.AccountPayments(ctx, d.Sub.Account, 50)
		if err != nil {
			return r, "", err
		}
		for _, p := range pays {
			// Money, not a gift: the back-office counts a conversion so.
			if p.Status == PaymentCredited && p.Provider != ProviderSupport && p.AmountXOF > 0 &&
				p.CreditedAt != nil && !p.CreditedAt.Before(t.StartedAt) {
				return r, NoteTrialConverted, nil
			}
		}
	}
	waiting, err := s.Store.WaitingPasses(ctx, d.Sub.Account)
	if err != nil {
		return r, "", err
	}
	if len(waiting) > 0 {
		return r, NoteWaitingPass, nil
	}
	return r, "", nil
}

// notifyApp sends the app notification of r and says how it went.
func (s *Service) notifyApp(ctx context.Context, a domain.AccountID, r reminder) (ReminderStatus, int, int) {
	if s.Notices == nil {
		return ReminderOff, 0, 0
	}
	n, err := s.Notices.NotifyAccount(ctx, a, r.title(), r.appText())
	switch {
	case errors.Is(err, ErrNoticesOff):
		return ReminderOff, 0, 0
	case err != nil:
		s.warn("licensing: renewal reminder: app notification failed", "account", a, "error", err)
		return ReminderFailed, 0, 0
	case n.Phones == 0:
		return ReminderNone, 0, 0
	case n.Sent == 0:
		return ReminderFailed, n.Phones, 0
	}
	return ReminderSent, n.Phones, n.Sent
}

// renewLink is where the WhatsApp reminder sends the person to renew:
// RenewURL, else the hub's purchase page with the box and the pass that
// renews the subscription's tier for a month, else nothing.
func (s *Service) renewLink(sub domain.Subscription) string {
	if s.RenewURL != "" {
		return s.RenewURL
	}
	if s.BuyURL == "" {
		return ""
	}
	q := url.Values{"box": {string(sub.BoxID)}}
	if pass, err := domain.FindPass(sub.Tier, domain.Term1Month); err == nil {
		q.Set("pass", pass.Code)
	}
	sep := "?"
	if strings.Contains(s.BuyURL, "?") {
		sep = "&"
	}
	return s.BuyURL + sep + q.Encode()
}

// reminder is one reminder's words.
type reminder struct {
	sub   domain.Subscription
	step  domain.ReminderStep
	trial bool
	// ended is a J0 reminder sent after the hour the pass ended.
	ended bool
}

var reminderTiers = map[domain.Tier]string{domain.TierBasic: "Basic", domain.TierMax: "Max"}

// what names the pass: « votre pass Max » or « votre essai de 7 jours de Max ».
func (r reminder) what() string {
	if r.trial {
		return "votre essai de 7 jours de Max"
	}
	return "votre pass " + reminderTiers[r.sub.Tier]
}

// when is the end, in Dakar: « le 28/11/2026 à 10h00 », or « aujourd'hui à
// 10h00 » on the day.
func (r reminder) when() string {
	end := r.sub.ExpiresAt.In(domain.Dakar)
	if r.step == domain.ReminderDay {
		return "aujourd'hui à " + end.Format("15h04")
	}
	return "le " + end.Format("02/01/2006 à 15h04")
}

// whatsApp is the WhatsApp message, with the link to renew when there is one.
func (r reminder) whatsApp(link string) string {
	var b strings.Builder
	b.WriteString("Kergui : " + r.what() + " pour la box " + string(r.sub.BoxID))
	if r.ended {
		b.WriteString(" a expiré " + r.when() + " : la box est repassée en Gratuit.")
	} else {
		b.WriteString(" expire " + r.when() + ".")
	}
	switch {
	case r.trial && link != "":
		b.WriteString(" Pour garder Max, prenez un pass ici : " + link)
	case r.trial:
		b.WriteString(" Pour garder Max, prenez un pass sur la page d'achat Kergui.")
	case r.ended && link != "":
		b.WriteString(" Reprenez un pass ici : " + link)
	case r.ended:
		b.WriteString(" Reprenez un pass sur la page d'achat Kergui.")
	case link != "":
		b.WriteString(" Prolongez-le ici (les jours s'ajoutent à la fin du pass en cours) : " + link)
	default:
		b.WriteString(" Prolongez-le sur la page d'achat Kergui : les jours s'ajoutent à la fin du pass en cours.")
	}
	return b.String()
}

// title is the app notification's title.
func (r reminder) title() string {
	name := "Votre pass"
	if r.trial {
		name = "Votre essai Max"
	}
	switch {
	case r.ended:
		return name + " est terminé"
	case r.step == domain.ReminderDay:
		return name + " se termine aujourd'hui"
	}
	return name + " se termine bientôt"
}

// appText is the app notification's text: when the pass ends, nothing about
// paying or where (RFC-006, No payment in the app).
func (r reminder) appText() string {
	what := "Le pass " + reminderTiers[r.sub.Tier]
	if r.trial {
		what = "L'essai de 7 jours de Max"
	}
	what += " de la box " + string(r.sub.BoxID)
	if r.ended {
		return what + " a pris fin " + r.when() + ". La box est repassée en Gratuit."
	}
	return what + " se termine " + r.when() + "."
}
