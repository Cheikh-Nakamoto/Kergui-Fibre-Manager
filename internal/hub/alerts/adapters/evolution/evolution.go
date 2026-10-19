// Package evolution talks to a self-hosted Evolution Go instance, the
// WhatsApp provider of the pilot (RFC-005: Evolution Go first, Meta Cloud
// API before paying customers).
//
// Three things about it shape this adapter:
//
//   - It is unofficial. It drives a WhatsApp Web session through whatsmeow,
//     so the number it sends from can be banned. That is why it is a
//     dedicated number and why the provider sits behind a port.
//   - It must be activated. A fresh instance answers 503 until it has been
//     registered in its Manager, and it keeps its activation alive with its
//     own heartbeats. A 503 is therefore reported as ErrNotActivated, not
//     as a transient failure, and Status lets the hub's health say so.
//   - Its licence (Apache 2.0 with brand conditions) requires a notice,
//     visible to administrators and reachable from the settings or the
//     documentation, saying that Evolution Go is in use. Attribution is
//     that notice; the hub returns it on the opt-in route the app's
//     settings screen reads, and docs/ repeats it.
//
// The routes are UNVERIFIED in the core's sense: they are what the project
// documents, not something proven against a live instance. The paths are
// fields so a deployment can correct them without a release, and the hub
// says so rather than pretending otherwise.
package evolution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// Notice is the "usage notification" Evolution Go's licence asks for.
const Notice = "Messagerie WhatsApp assurée par Evolution Go (evolution-api.com)."

// Defaults of the documented API.
const (
	// DefaultSendPath is the documented send route. It is UNVERIFIED.
	DefaultSendPath = "/message/sendText"
	// DefaultStatusPath answers whether the instance is connected.
	DefaultStatusPath = "/instance/connectionState"
	// APIKeyHeader is the header the instance's key travels in.
	APIKeyHeader = "apikey"
	// maxInboundBytes bounds a webhook body.
	maxInboundBytes = 256 << 10
)

// Config wires the adapter.
type Config struct {
	// BaseURL of the Evolution Go instance, e.g. http://evolution:8080.
	BaseURL string
	// Instance is the session name. Every inbound event must name it:
	// one hub serves one dedicated number.
	Instance string
	// APIKey authenticates the hub to the instance. It is never logged.
	APIKey string
	// SendPath and StatusPath override the documented routes.
	SendPath   string
	StatusPath string
	HTTP       *http.Client
	Now        func() time.Time
}

// Notifier is the Evolution Go provider.
type Notifier struct {
	cfg  Config
	http *http.Client
	now  func() time.Time
}

var _ domain.Notifier = (*Notifier)(nil)

// New wires the adapter. It validates only what it can: a usable base URL,
// an instance name and a key.
func New(cfg Config) (*Notifier, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if u, err := url.Parse(base); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("evolution: invalid base URL %q", cfg.BaseURL)
	}
	if strings.TrimSpace(cfg.Instance) == "" {
		return nil, errors.New("evolution: the instance name is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("evolution: the instance API key is required")
	}
	cfg.BaseURL = base
	if cfg.SendPath == "" {
		cfg.SendPath = DefaultSendPath
	}
	if cfg.StatusPath == "" {
		cfg.StatusPath = DefaultStatusPath
	}
	client := cfg.HTTP
	if client == nil {
		// A message is not worth holding a request open for: the household
		// is told by push too, and the next alert comes soon enough.
		client = &http.Client{Timeout: 15 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Notifier{cfg: cfg, http: client, now: now}, nil
}

// Name identifies the provider.
func (n *Notifier) Name() string { return "evolution" }

// Attribution is the notice the licence requires.
func (n *Notifier) Attribution() string { return Notice }

type sendRequest struct {
	Number   string `json:"number"`
	Text     string `json:"text"`
	Instance string `json:"instance,omitempty"`
}

type sendResponse struct {
	Key struct {
		ID string `json:"id"`
	} `json:"key"`
	MessageID string `json:"messageId,omitempty"`
}

// Send posts one text message.
func (n *Notifier) Send(ctx context.Context, to domain.Phone, msg domain.OutboundMessage) (string, error) {
	body, err := json.Marshal(sendRequest{
		// WhatsApp wants the digits without the leading plus.
		Number: strings.TrimPrefix(string(to), "+"), Text: msg.Text, Instance: n.cfg.Instance,
	})
	if err != nil {
		return "", fmt.Errorf("evolution: encode message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.BaseURL+n.cfg.SendPath, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("evolution: send: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(APIKeyHeader, n.cfg.APIKey)

	resp, err := n.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("evolution: send: %w", err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(http.MaxBytesReader(nil, resp.Body, maxInboundBytes))

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		// The instance is up but not licensed or not registered yet.
		return "", fmt.Errorf("%w: %s answered 503", domain.ErrNotActivated, n.Name())
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		// Never echo the key, not even its length.
		return "", errors.New("evolution: the instance refused the API key")
	case resp.StatusCode >= 300:
		return "", fmt.Errorf("evolution: send returned %d: %s", resp.StatusCode, firstLine(payload))
	}
	var out sendResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		// A message that went out but whose id we cannot read is still
		// sent: the household has it, and only the reply-to-this-alert
		// shortcut is lost.
		return "", nil
	}
	if out.Key.ID != "" {
		return out.Key.ID, nil
	}
	return out.MessageID, nil
}

// Status reports whether the instance can send. The hub's health uses it,
// so an administrator sees "activation required" instead of households
// quietly receiving nothing.
func (n *Notifier) Status(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, n.cfg.BaseURL+n.cfg.StatusPath, nil)
	if err != nil {
		return fmt.Errorf("evolution: status: %w", err)
	}
	req.Header.Set(APIKeyHeader, n.cfg.APIKey)
	resp, err := n.http.Do(req)
	if err != nil {
		return fmt.Errorf("evolution: status: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxInboundBytes))
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return fmt.Errorf("%w: register the instance in its Manager", domain.ErrNotActivated)
	case resp.StatusCode >= 300:
		return fmt.Errorf("evolution: status returned %d", resp.StatusCode)
	}
	return nil
}

// webhookEvent is the part of an Evolution Go webhook this hub reads.
// Unknown fields are ignored on purpose: the provider adds events, and a
// hub that refused the ones it does not know would break on an upgrade.
type webhookEvent struct {
	Event    string `json:"event"`
	Instance string `json:"instance"`
	Data     struct {
		Key struct {
			RemoteJID string `json:"remoteJid"`
			FromMe    bool   `json:"fromMe"`
			ID        string `json:"id"`
		} `json:"key"`
		PushName    string `json:"pushName"`
		MessageType string `json:"messageType"`
		Message     struct {
			Conversation string `json:"conversation"`
			Extended     struct {
				Text    string `json:"text"`
				Context struct {
					StanzaID string `json:"stanzaId"`
				} `json:"contextInfo"`
			} `json:"extendedTextMessage"`
		} `json:"message"`
		MessageTimestamp int64 `json:"messageTimestamp"`
	} `json:"data"`
}

// ParseInbound reads a message out of a webhook call.
//
// Evolution Go does not sign its webhooks — it offers a URL and nothing
// else — so authenticity cannot be established here. It is established by
// the route: the hub mounts the webhook on a path carrying a secret, and
// compares it in constant time (see the alerts handler). What this method
// still refuses is everything that is not a person writing to us: our own
// messages, group chats, receipts and events of another instance.
func (n *Notifier) ParseInbound(r *http.Request) (domain.InboundMessage, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxInboundBytes))
	if err != nil {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	var ev webhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	if !strings.EqualFold(ev.Instance, n.cfg.Instance) {
		// Another session's traffic has no business here, whatever secret
		// the caller knew.
		return domain.InboundMessage{}, domain.ErrNotAuthentic
	}
	if ev.Data.Key.FromMe {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	phone, ok := phoneFromJID(ev.Data.Key.RemoteJID)
	if !ok {
		// A group ("…@g.us") or a channel: alerts are a conversation with
		// one person.
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	text := ev.Data.Message.Conversation
	if text == "" {
		text = ev.Data.Message.Extended.Text
	}
	if strings.TrimSpace(text) == "" {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	at := n.now().UTC()
	if ev.Data.MessageTimestamp > 0 {
		at = time.Unix(ev.Data.MessageTimestamp, 0).UTC()
	}
	return domain.InboundMessage{
		From: phone, Text: text,
		QuotedProviderID: ev.Data.Message.Extended.Context.StanzaID,
		ProviderID:       ev.Data.Key.ID,
		At:               at,
	}, nil
}

// phoneFromJID reads a number out of a WhatsApp id ("221771234567@s.whatsapp.net").
func phoneFromJID(jid string) (domain.Phone, bool) {
	user, domainPart, found := strings.Cut(jid, "@")
	if !found || !strings.HasPrefix(domainPart, "s.whatsapp.net") {
		return "", false
	}
	// A JID may carry a device suffix ("…:12@s.whatsapp.net").
	user, _, _ = strings.Cut(user, ":")
	phone, err := domain.ParsePhone("+" + user)
	if err != nil {
		return "", false
	}
	return phone, true
}

// firstLine keeps an error readable without pasting a whole HTML page into
// the hub's log.
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
