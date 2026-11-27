// Package domain holds what an alert is and the two ports that carry one:
// a messaging provider (WhatsApp) and app push.
//
// Both are ports with one meaning: the hub hands over a text it composed
// from a signed snapshot, and reads back what the household answered. No
// provider decides anything, and no provider is trusted to say who the
// household is — a reply is tied to a verified number and to the alert it
// answers, never to what the message says.
package domain

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Refusals the handlers and use cases map to status codes.
var (
	// ErrNotActivated is a provider that is installed but not licensed or
	// not linked yet (whatsmeow before its number is paired). A hub that
	// cannot send must say so rather than drop the household's alerts
	// silently.
	ErrNotActivated = errors.New("alerts: the messaging provider is not activated")
	// ErrNotAuthentic is an inbound webhook that did not prove where it
	// came from.
	ErrNotAuthentic = errors.New("alerts: the inbound message is not authentic")
	// ErrIgnored is a well-formed inbound event that is none of our
	// business (a delivery receipt, a message we sent, a group chat).
	ErrIgnored = errors.New("alerts: nothing to do with this inbound event")
	// ErrUnknownPhone is a message from a number no household registered.
	ErrUnknownPhone = errors.New("alerts: this number is not opted in")
	ErrInvalidPhone = errors.New("alerts: not a usable phone number")
	ErrNoOptIn      = errors.New("alerts: no opt-in is pending for this code")
	ErrNoAlert      = errors.New("alerts: no open alert to answer")
	// ErrAmbiguousAlert is a reply that quotes no alert of ours while
	// several are open for that number: the hub asks which one rather than
	// guess (audit C2).
	ErrAmbiguousAlert = errors.New("alerts: the reply quotes no alert and several are open")
)

// Phone is a WhatsApp number in E.164 ("+221771234567"). It is a distinct
// type because it must never be logged or echoed in full: Masked is what
// goes into a log line or an error.
type Phone string

const (
	senegalCountryCode  = "221"
	senegalMobileDigits = 9
)

// ParsePhone normalises a number to E.164. It accepts the spacing and
// punctuation people actually type, and refuses anything that is not a
// plausible international number.
func ParsePhone(s string) (Phone, error) {
	var digits strings.Builder
	s = strings.TrimSpace(s)
	international := strings.HasPrefix(s, "+") || strings.HasPrefix(s, "00")
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits.WriteRune(r)
		case r == '+' || r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
			// Punctuation people type; the leading + is implied by E.164.
		default:
			return "", ErrInvalidPhone
		}
	}
	d := digits.String()
	// A number typed with the international prefix instead of a plus
	// ("00221…") is the same number: no country code starts with a zero,
	// so stripping it cannot change which number this is.
	if strings.HasPrefix(d, "00") && len(d) > 10 {
		d = d[2:]
	}
	// The households are in Senegal, where a mobile is said and typed as
	// nine digits starting with 7 ("77 123 45 67"). Without a plus or 00,
	// that is a local number: no international number is nine digits
	// starting with 7 (country code 7 has ten more).
	if !international && len(d) == senegalMobileDigits && d[0] == '7' {
		d = senegalCountryCode + d
	}
	// E.164 allows up to 15 digits; below 8 it cannot be an international
	// number, and a WhatsApp number always is.
	if len(d) < 8 || len(d) > 15 || strings.HasPrefix(d, "0") {
		return "", ErrInvalidPhone
	}
	return Phone("+" + d), nil
}

// Masked is the number as it may appear in a log or an error: enough to
// recognise one's own number, not enough to be a contact list.
func (p Phone) Masked() string {
	s := string(p)
	if len(s) < 6 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-2:]
}

// OutboundMessage is a message the hub sends.
type OutboundMessage struct {
	Text string
	// AlertID is the alert this message is about, so a provider that echoes
	// a quoted id can be matched back to it. It is not sent anywhere.
	AlertID string
}

// InboundMessage is what a household answered, as a provider reports it.
// Everything in it is untrusted input except From, which the provider
// authenticates, and even that only identifies a number — what it may do is
// decided from the hub's own records.
type InboundMessage struct {
	From Phone
	Text string
	// QuotedProviderID is the id of the message being replied to, when the
	// provider reports one. It is how a reply is tied to its alert even
	// when several are open.
	QuotedProviderID string
	ProviderID       string
	At               time.Time
}

// Keyword is the intent read out of an inbound message.
type Keyword int

const (
	// KeywordNone is a message that asks for nothing we handle.
	KeywordNone Keyword = iota
	// KeywordOptIn is the "KERGUI <code>" message the wa.me link pre-fills.
	KeywordOptIn
	// KeywordStop ends the alerts for that number.
	KeywordStop
	// KeywordChoice is one or more device numbers ("1", "1 2"): block them.
	// A bare "2" on an alert about one device, where it cannot be a device
	// number, still approves it, for the households that learned it (issue
	// #58); the alerts no longer teach it (audit C2).
	KeywordChoice
	// KeywordApprove is "OK" or "APPROUVER": approve every device of the
	// alert answered, so none of them is signalled again (issue #58). It is
	// the one word that authorises.
	KeywordApprove
)

// Intent is what an inbound message asks for.
type Intent struct {
	Keyword Keyword
	// Code is the pairing code of an opt-in message.
	Code string
	// Choices are the device numbers of a reply, in the order they were
	// written, without repeats.
	Choices []int
}

// ReadIntent reads an inbound text. It is deliberately forgiving about case,
// accents and spacing — the people this is for type on a phone keyboard —
// and deliberately strict about what counts: a number is a device number of
// the alert being answered, never an address the message carries.
func ReadIntent(text string) Intent {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(text)))
	if len(fields) == 0 {
		return Intent{}
	}
	switch fields[0] {
	case "KERGUI":
		if len(fields) > 1 {
			return Intent{Keyword: KeywordOptIn, Code: fields[1]}
		}
		return Intent{Keyword: KeywordOptIn}
	case "STOP", "ARRET", "ARRÊT":
		return Intent{Keyword: KeywordStop}
	case "OK", "APPROUVER", "APPROUVE", "APPROUVÉ":
		if len(fields) == 1 {
			return Intent{Keyword: KeywordApprove}
		}
	}
	var choices []int
	seen := map[int]bool{}
	for _, f := range fields {
		n := 0
		for _, r := range f {
			if !unicode.IsDigit(r) {
				return Intent{} // "1er étage" is not a choice
			}
			n = n*10 + int(r-'0')
			if n > 99 {
				return Intent{}
			}
		}
		if n == 0 || seen[n] {
			continue
		}
		seen[n] = true
		choices = append(choices, n)
	}
	if len(choices) == 0 {
		return Intent{}
	}
	return Intent{Keyword: KeywordChoice, Choices: choices}
}

// Notifier is a messaging provider.
type Notifier interface {
	// Name identifies the provider in logs and in the audit trail.
	Name() string
	// Attribution is the notice the provider's licence requires the hub to
	// show its administrators. It is empty for a provider that requires
	// none (whatsmeow, console).
	Attribution() string
	// Send delivers a message and returns the provider's id for it, which
	// is what ties a later reply to this alert.
	Send(ctx context.Context, to Phone, msg OutboundMessage) (providerID string, err error)
	// ParseInbound authenticates an incoming webhook request and reads the
	// message out of it. It returns ErrNotAuthentic when it cannot prove
	// where the request came from, and ErrIgnored for an event that is not
	// a message from a person.
	ParseInbound(r *http.Request) (InboundMessage, error)
}

// InboundSource is a provider that receives messages itself rather than
// through a webhook: a WhatsApp Web session running inside the hub (issue
// #59). The composition root hands it the alerts module's handler.
type InboundSource interface {
	OnInbound(deliver func(context.Context, InboundMessage))
}

// Pusher is the second channel: a notification to the household's app,
// which reaches it with the app closed (issue #49). The console adapter
// logs; the Expo adapter hands it to Expo's push service, which carries it
// over FCM (Android) and APNs (iOS).
type Pusher interface {
	Name() string
	// Push delivers one notification. ErrPushTokenGone means the
	// installation is no more (the app was uninstalled, or its token
	// rotated): the hub forgets the token rather than push to it forever.
	Push(ctx context.Context, to PushTarget, msg PushMessage) error
}

// ErrPushTokenGone is a push token the provider says no longer exists.
var ErrPushTokenGone = errors.New("alerts: the push token is no longer registered")

// ErrInvalidPushToken is a registration the hub will not store.
var ErrInvalidPushToken = errors.New("alerts: not a usable push token")

// Push platforms.
const (
	PlatformAndroid = "android"
	PlatformIOS     = "ios"
)

// maxPushTokenLen bounds a token: an Expo token is ~41 characters, a raw
// FCM one ~160; anything past this is not a token.
const maxPushTokenLen = 512

// PushTarget is one app installation.
type PushTarget struct {
	TenantID string
	UserID   string
	Platform string // "android" | "ios"
	Token    string
}

// Validate checks a registration before it is stored: a known platform and
// a token that is one printable word.
func (t PushTarget) Validate() error {
	if t.Platform != PlatformAndroid && t.Platform != PlatformIOS {
		return fmt.Errorf("%w: platform %q", ErrInvalidPushToken, t.Platform)
	}
	if t.Token == "" || len(t.Token) > maxPushTokenLen {
		return ErrInvalidPushToken
	}
	for _, r := range t.Token {
		if r <= ' ' || r > '~' {
			return ErrInvalidPushToken
		}
	}
	return nil
}

// PushMessage is what the app shows.
type PushMessage struct {
	Title string
	Body  string
	// Data is what the app needs to open the right screen: the alert, the
	// household and the device's MAC (the device sheet), never a
	// credential.
	Data map[string]string
	// Category is the app's notification category, whose buttons the
	// notification shows ("" for none).
	Category string
}

// PushTargets lists where to push, and keeps the tokens the app registers.
type PushTargets interface {
	// For lists a household's installations.
	For(ctx context.Context, tenantID string) ([]PushTarget, error)
	// Register stores (or refreshes) one installation's token for a
	// member of a household.
	Register(ctx context.Context, t PushTarget, now time.Time) error
	// Unregister drops a member's token for a household (sign-out, alerts
	// turned off).
	Unregister(ctx context.Context, tenantID, userID, token string) error
	// Forget drops a token everywhere: the provider says it is gone. (A
	// member removed from a household loses its tokens there with its
	// WhatsApp opt-in, Repo.StopMember.)
	Forget(ctx context.Context, token string) error
}
