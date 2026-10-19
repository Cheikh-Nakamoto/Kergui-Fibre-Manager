// Package console is the pilot notifier: it writes the messages it would
// send to the hub's log and keeps them in memory.
//
// It exists for three reasons. It is what CI and the end-to-end test alert
// through, so the whole path — snapshot, diff, alert, reply, block — is
// exercised without a WhatsApp account. It is what a self-hosted hub falls
// back to before a provider is configured, so alerts are visible rather
// than silently dropped. And its inbound side is signed (HMAC-SHA256),
// which gives the "1 = block" path an authenticated webhook to be tested
// against, independently of what any provider offers.
package console

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// SignatureHeader carries the HMAC of the body.
const SignatureHeader = "X-Kergui-Signature"

// maxInboundBytes bounds an inbound webhook body. A message is a line of
// text; anything larger is a mistake or an attempt to make the hub chew.
const maxInboundBytes = 64 << 10

// Notifier is the console provider.
type Notifier struct {
	// Secret signs the inbound webhook. Without one, inbound messages are
	// refused: an unauthenticated route that can block a device is not a
	// convenience worth having.
	Secret []byte
	Log    *slog.Logger
	Now    func() time.Time

	mu   sync.Mutex
	sent []Sent
}

// Sent is one message the console "delivered".
type Sent struct {
	To        domain.Phone
	Text      string
	AlertID   string
	MessageID string
	At        time.Time
}

var _ domain.Notifier = (*Notifier)(nil)

// Name identifies the provider.
func (n *Notifier) Name() string { return "console" }

// Attribution is empty: there is no third party to credit here.
func (n *Notifier) Attribution() string { return "" }

// Send logs the message and remembers it.
func (n *Notifier) Send(_ context.Context, to domain.Phone, msg domain.OutboundMessage) (string, error) {
	now := time.Now
	if n.Now != nil {
		now = n.Now
	}
	at := now().UTC()
	id := fmt.Sprintf("console-%d", at.UnixNano())

	n.mu.Lock()
	n.sent = append(n.sent, Sent{To: to, Text: msg.Text, AlertID: msg.AlertID, MessageID: id, At: at})
	n.mu.Unlock()

	if n.Log != nil {
		// The number is masked and the text is not logged: an alert names
		// the household's devices, and a log is not the place for that.
		n.Log.Info("alerts: message not sent, no provider configured",
			"to", to.Masked(), "alert_id", msg.AlertID, "message_id", id, "lines", strings.Count(msg.Text, "\n")+1)
	}
	return id, nil
}

// Sent returns the messages delivered so far, newest last.
func (n *Notifier) Sent() []Sent {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Sent(nil), n.sent...)
}

// LastTo returns the newest message sent to a number.
func (n *Notifier) LastTo(to domain.Phone) (Sent, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.sent) - 1; i >= 0; i-- {
		if n.sent[i].To == to {
			return n.sent[i], true
		}
	}
	return Sent{}, false
}

// inbound is the body the console webhook accepts: one message, as a
// provider would report it.
type inbound struct {
	From     string `json:"from"`
	Text     string `json:"text"`
	QuotedID string `json:"quotedId,omitempty"`
	ID       string `json:"id,omitempty"`
}

// ParseInbound authenticates the request with the shared secret and reads
// the message. The signature covers the exact bytes received, so a body
// altered on the way is refused.
func (n *Notifier) ParseInbound(r *http.Request) (domain.InboundMessage, error) {
	if len(n.Secret) == 0 {
		return domain.InboundMessage{}, domain.ErrNotAuthentic
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxInboundBytes))
	if err != nil {
		return domain.InboundMessage{}, domain.ErrNotAuthentic
	}
	if !n.validSignature(r.Header.Get(SignatureHeader), body) {
		return domain.InboundMessage{}, domain.ErrNotAuthentic
	}
	var in inbound
	if err := json.Unmarshal(body, &in); err != nil {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	phone, err := domain.ParsePhone(in.From)
	if err != nil {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	if strings.TrimSpace(in.Text) == "" {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	at := time.Now().UTC()
	if n.Now != nil {
		at = n.Now().UTC()
	}
	return domain.InboundMessage{
		From: phone, Text: in.Text, QuotedProviderID: in.QuotedID,
		ProviderID: in.ID, At: at,
	}, nil
}

// validSignature checks "sha256=<hex>" in constant time.
func (n *Notifier) validSignature(header string, body []byte) bool {
	hexSig, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, n.Secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign is what a client (a test, or a curl in the pilot) puts in the
// signature header for a body.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Pusher is the console push channel: the port H3's app notifications plug
// into, logging for now.
type Pusher struct {
	Log *slog.Logger

	mu   sync.Mutex
	sent []PushedMessage
}

// PushedMessage is one notification the console "pushed".
type PushedMessage struct {
	Target domain.PushTarget
	Msg    domain.PushMessage
}

var _ domain.Pusher = (*Pusher)(nil)

// Name identifies the channel.
func (p *Pusher) Name() string { return "console" }

// Push logs the notification and remembers it.
func (p *Pusher) Push(_ context.Context, to domain.PushTarget, msg domain.PushMessage) error {
	p.mu.Lock()
	p.sent = append(p.sent, PushedMessage{Target: to, Msg: msg})
	p.mu.Unlock()
	if p.Log != nil {
		p.Log.Info("alerts: push not delivered, no push provider configured",
			"user_id", to.UserID, "platform", to.Platform, "title", msg.Title)
	}
	return nil
}

// Pushed returns the notifications handed over so far.
func (p *Pusher) Pushed() []PushedMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]PushedMessage(nil), p.sent...)
}
