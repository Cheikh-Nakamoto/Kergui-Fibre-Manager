// Package evolution talks to a self-hosted Evolution Go instance, the
// WhatsApp provider of the pilot (RFC-005: Evolution Go first, Meta Cloud
// API before paying customers).
//
// Three things about it shape this adapter:
//
//   - It is unofficial. It drives a WhatsApp Web session through whatsmeow,
//     so the number it sends from can be banned. That is why it is a
//     dedicated number and why the provider sits behind a port.
//   - It must be activated. Until its licence is activated in its Manager
//     (/manager/login), every route answers 503 LICENSE_REQUIRED, and it
//     keeps its activation alive with its own heartbeats. A 503 is
//     therefore reported as ErrNotActivated, not as a transient failure,
//     and Status lets the hub's health say so.
//   - Its licence (Apache 2.0 with brand conditions) requires a notice,
//     visible to administrators and reachable from the settings or the
//     documentation, saying that Evolution Go is in use. Attribution is
//     that notice; the hub returns it on the opt-in route the app's
//     settings screen reads, and docs/ repeats it.
//
// # What is verified, and against what
//
// Evolution Go is not the older Evolution API (Node): its README still
// lists the Node routes, but its code does not serve them. Everything below
// was VERIFIED AGAINST THE SOURCE of github.com/evolution-foundation/evolution-go,
// branch main, read on 2026-11-22 — not against a running instance:
//
//   - Auth (pkg/middleware/auth_middleware.go): header apikey. Instance
//     routes take the instance's own token and find the instance from it
//     (GetInstanceByToken); the instance's name is never in the path. Admin
//     routes (/instance/create, /instance/all…) take GLOBAL_API_KEY. A
//     refused key gets 401 {"error":"not authorized"}.
//   - Send (pkg/routes/routes.go, pkg/sendMessage): POST /send/text with
//     {"number","text"}; the answer is {"message":"success","data":
//     {"Info":{…,"ID":…},"Message":…}}, read tolerantly since it is not
//     documented.
//   - Status (pkg/instance): GET /instance/status answers {"message":
//     "success","data":{"Connected":…,"LoggedIn":…,"Name":…}} (untagged Go
//     fields, so PascalCase; encoding/json matches keys case-insensitively,
//     which also covers lower case).
//   - Activation (pkg/core/c0.go): 503 {"error":"service not activated",
//     "code":"LICENSE_REQUIRED","register_url":…,"message":…} on every route.
//   - Inbound (pkg/whatsmeow/service/whatsmeow.go, myEventHandler): a
//     MESSAGE subscription posts {"event":"Message","data":<whatsmeow
//     events.Message as encoding/json>,"instanceToken","instanceId",
//     "instanceName"}; non-2xx answers are retried.
//
// VERIFIED AGAINST A LIVE INSTANCE on 2026-11-22 (official image
// evoapicloud/evolution-go, licence not yet activated): the routes above
// exist, /message/sendText does not, and every route answers the 503
// LICENSE_REQUIRED body above, which Status reports as ErrNotActivated with
// the Manager's URL.
//
// NOT VERIFIED until an activated, linked instance has run them: the exact
// send answer, the 401 on a wrong token, a real MESSAGE event, LID-only
// senders (no phone number anywhere in the event) and whether a given
// WhatsApp client puts a reply's quoted id where whatsmeow does. The paths
// stay fields so a deployment can correct them without a release.
package evolution

import (
	"bytes"
	"context"
	"crypto/subtle"
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

// Defaults of Evolution Go's API (verified against its source, see above).
const (
	// DefaultSendPath sends one text message as the token's instance.
	DefaultSendPath = "/send/text"
	// DefaultStatusPath answers whether the token's instance is connected
	// and logged in to WhatsApp.
	DefaultStatusPath = "/instance/status"
	// APIKeyHeader is the header the instance token travels in.
	APIKeyHeader = "apikey"
	// eventMessage is the event name of a received message.
	eventMessage = "Message"
	// maxInboundBytes bounds a webhook body.
	maxInboundBytes = 256 << 10
)

var (
	// ErrUnauthorized is Evolution Go refusing the key. On the routes this
	// adapter uses, that is almost always GLOBAL_API_KEY configured where
	// the instance's token belongs.
	ErrUnauthorized = errors.New("evolution: the instance refused the key (KERGUI_HUB_EVOLUTION_APIKEY must be the instance token, not GLOBAL_API_KEY)")
	// ErrNotConnected is an activated instance whose WhatsApp session is
	// not up: its QR code has not been scanned, or the phone logged out.
	ErrNotConnected = errors.New("evolution: the instance is not logged in to WhatsApp (scan its QR code: GET /instance/qr)")
)

// Config wires the adapter.
type Config struct {
	// BaseURL of the Evolution Go instance, e.g. http://evolution:8080.
	BaseURL string
	// Instance is the instance's name, as given at POST /instance/create.
	// The token already picks the instance on outbound calls; the name is
	// what every inbound event must carry: one hub serves one dedicated
	// number.
	Instance string
	// APIKey is the instance's token (chosen at POST /instance/create), not
	// Evolution Go's GLOBAL_API_KEY. It is never logged.
	APIKey string
	// SendPath and StatusPath override the default routes.
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
// an instance name and a token.
func New(cfg Config) (*Notifier, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if u, err := url.Parse(base); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("evolution: invalid base URL %q", cfg.BaseURL)
	}
	if strings.TrimSpace(cfg.Instance) == "" {
		return nil, errors.New("evolution: the instance name is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("evolution: the instance token is required")
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

// sendRequest is the body of POST /send/text (send_service.TextStruct).
// The optional fields (id, delay, formatJid, mentions, quoted) are left to
// Evolution Go's defaults.
type sendRequest struct {
	Number string `json:"number"`
	Text   string `json:"text"`
}

// sendResponse reads the message id wherever it may be. Evolution Go
// answers {"data":{"Info":{"ID":…}}}; the other two shapes are the older
// Evolution API's, kept because the answer is not documented and costs
// nothing to tolerate.
type sendResponse struct {
	Data *struct {
		Info struct {
			ID string `json:"ID"`
		} `json:"Info"`
	} `json:"data"`
	Key struct {
		ID string `json:"id"`
	} `json:"key"`
	MessageID string `json:"messageId,omitempty"`
}

// Send posts one text message.
func (n *Notifier) Send(ctx context.Context, to domain.Phone, msg domain.OutboundMessage) (string, error) {
	body, err := json.Marshal(sendRequest{
		// WhatsApp wants the digits without the leading plus.
		Number: strings.TrimPrefix(string(to), "+"), Text: msg.Text,
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

	if err := n.refusal(resp.StatusCode, payload); err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("evolution: send returned %d: %s", resp.StatusCode, firstLine(payload))
	}
	var out sendResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		// A message that went out but whose id we cannot read is still
		// sent: the household has it, and only the reply-to-this-alert
		// shortcut is lost.
		return "", nil
	}
	switch {
	case out.Data != nil && out.Data.Info.ID != "":
		return out.Data.Info.ID, nil
	case out.Key.ID != "":
		return out.Key.ID, nil
	}
	return out.MessageID, nil
}

// statusResponse is GET /instance/status's answer.
type statusResponse struct {
	Data *struct {
		Connected bool
		LoggedIn  bool
	} `json:"data"`
}

// Status reports whether the instance can send. The hub's health uses it,
// so an administrator sees "activation required" or "scan the QR code"
// instead of households quietly receiving nothing.
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
	payload, _ := io.ReadAll(http.MaxBytesReader(nil, resp.Body, maxInboundBytes))
	if err := n.refusal(resp.StatusCode, payload); err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("evolution: status returned %d: %s", resp.StatusCode, firstLine(payload))
	}
	var out statusResponse
	if err := json.Unmarshal(payload, &out); err != nil || out.Data == nil {
		return errors.New("evolution: status: unreadable answer")
	}
	if !out.Data.Connected || !out.Data.LoggedIn {
		return ErrNotConnected
	}
	return nil
}

// licenceRequired is the 503 body Evolution Go answers until activated.
type licenceRequired struct {
	Code        string `json:"code"`
	RegisterURL string `json:"register_url"`
}

// refusal maps the answers that are a state of the instance rather than a
// failure of this call: not activated, or the key refused.
func (n *Notifier) refusal(code int, payload []byte) error {
	switch code {
	case http.StatusServiceUnavailable:
		var body licenceRequired
		_ = json.Unmarshal(payload, &body)
		if u, err := url.Parse(body.RegisterURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
			// The Manager's own URL, as Evolution Go built it from the
			// request's Host: it carries no secret.
			return fmt.Errorf("%w: activate the licence at %s", domain.ErrNotActivated, firstLine([]byte(u.String())))
		}
		return fmt.Errorf("%w: activate the licence in the Evolution Go Manager (/manager/login)", domain.ErrNotActivated)
	case http.StatusUnauthorized, http.StatusForbidden:
		// Never echo the key, not even its length.
		return ErrUnauthorized
	}
	return nil
}

// webhookEvent is the part of an Evolution Go webhook this hub reads: the
// envelope myEventHandler builds around whatsmeow's events.Message. Info's
// keys are types.MessageInfo's untagged fields; Message's are the protobuf's
// json tags. Unknown fields are ignored on purpose: the provider adds
// events, and a hub that refused the ones it does not know would break on
// an upgrade.
type webhookEvent struct {
	Event         string `json:"event"`
	InstanceName  string `json:"instanceName"`
	InstanceToken string `json:"instanceToken"`
	Data          struct {
		Info struct {
			Chat      string `json:"Chat"`
			Sender    string `json:"Sender"`
			SenderAlt string `json:"SenderAlt"`
			IsFromMe  bool   `json:"IsFromMe"`
			IsGroup   bool   `json:"IsGroup"`
			ID        string `json:"ID"`
			// Timestamp is a time.Time marshalled as RFC 3339. It is read
			// as a string so that a malformed one loses the time only, not
			// the message.
			Timestamp string `json:"Timestamp"`
		} `json:"Info"`
		Message struct {
			Conversation string `json:"conversation"`
			Extended     struct {
				Text    string `json:"text"`
				Context struct {
					StanzaID string `json:"stanzaID"`
				} `json:"contextInfo"`
			} `json:"extendedTextMessage"`
		} `json:"Message"`
	} `json:"data"`
}

// ParseInbound reads a message out of a webhook call.
//
// Evolution Go does not sign its webhooks — it offers a URL and nothing
// else — so authenticity rests first on the route: the hub mounts the
// webhook on a path carrying a secret and compares it in constant time (see
// the alerts handler). On top of it, every event carries the instance's
// name and token; both must be ours. What this method then refuses is
// everything that is not a person writing to us: other events, our own
// messages, groups, broadcasts, and messages without text.
func (n *Notifier) ParseInbound(r *http.Request) (domain.InboundMessage, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxInboundBytes))
	if err != nil {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	var ev webhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	if !strings.EqualFold(ev.InstanceName, n.cfg.Instance) ||
		subtle.ConstantTimeCompare([]byte(ev.InstanceToken), []byte(n.cfg.APIKey)) != 1 {
		// Another session's traffic has no business here, whatever secret
		// the caller knew.
		return domain.InboundMessage{}, domain.ErrNotAuthentic
	}
	if ev.Event != eventMessage {
		// Receipts, presences, SendMessage (our own sends)…
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	info := ev.Data.Info
	if info.IsFromMe || info.IsGroup || !isDirectChat(info.Chat) {
		// Alerts are a conversation with one person.
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	// Evolution Go puts the phone JID in Sender when whatsmeow reports both
	// a LID and a phone number; SenderAlt and Chat are the fall-backs. A
	// sender known only by its LID has no number we could match.
	phone, ok := phoneFromJID(info.Sender)
	if !ok {
		phone, ok = phoneFromJID(info.SenderAlt)
	}
	if !ok {
		phone, ok = phoneFromJID(info.Chat)
	}
	if !ok {
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
	if t, err := time.Parse(time.RFC3339Nano, info.Timestamp); err == nil && !t.IsZero() {
		at = t.UTC()
	}
	return domain.InboundMessage{
		From: phone, Text: text,
		QuotedProviderID: ev.Data.Message.Extended.Context.StanzaID,
		ProviderID:       info.ID,
		At:               at,
	}, nil
}

// isDirectChat is a one-to-one chat: a phone JID or a LID, not a group, a
// broadcast list, a status or a newsletter.
func isDirectChat(jid string) bool {
	_, server, found := strings.Cut(jid, "@")
	return found && (server == "s.whatsapp.net" || server == "lid")
}

// phoneFromJID reads a number out of a WhatsApp id ("221771234567@s.whatsapp.net").
func phoneFromJID(jid string) (domain.Phone, bool) {
	user, server, found := strings.Cut(jid, "@")
	if !found || server != "s.whatsapp.net" {
		return "", false
	}
	// A JID may carry a device suffix ("…:12@s.whatsapp.net") or an agent
	// ("….1:12@…").
	user, _, _ = strings.Cut(user, ":")
	user, _, _ = strings.Cut(user, ".")
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
