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
	"net/http"
	"strings"
	"time"
	"unicode"
)

// Refusals the handlers and use cases map to status codes.
var (
	// ErrNotActivated is a provider that is installed but not licensed or
	// not registered yet. Evolution Go answers 503 until its instance is
	// activated, and a hub that cannot send must say so rather than drop
	// the household's alerts silently.
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
)

// Phone is a WhatsApp number in E.164 ("+221771234567"). It is a distinct
// type because it must never be logged or echoed in full: Masked is what
// goes into a log line or an error.
type Phone string

// ParsePhone normalises a number to E.164. It accepts the spacing and
// punctuation people actually type, and refuses anything that is not a
// plausible international number.
func ParsePhone(s string) (Phone, error) {
	var digits strings.Builder
	for _, r := range strings.TrimSpace(s) {
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
	KeywordChoice
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
	// show its administrators (Evolution Go's "Usage notification"). It is
	// empty for a provider that requires none.
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

// Pusher is the second channel: a notification to the household's app.
// H2 ships the port and a console adapter; the real one lands with the app
// in H3, which is also when devices start registering tokens.
type Pusher interface {
	Name() string
	Push(ctx context.Context, to PushTarget, msg PushMessage) error
}

// PushTarget is one app installation.
type PushTarget struct {
	UserID   string
	Platform string // "android" | "ios"
	Token    string
}

// PushMessage is what the app shows.
type PushMessage struct {
	Title string
	Body  string
	// Data is what the app needs to open the right screen: the alert and
	// the household, never a credential.
	Data map[string]string
}

// PushTargets lists where to push. It is a port with no implementation in
// H2: the hub has no device tokens until the app registers them (H3), so
// the alert path already walks through it and finds nobody.
type PushTargets interface {
	For(ctx context.Context, tenantID, userID string) ([]PushTarget, error)
}
