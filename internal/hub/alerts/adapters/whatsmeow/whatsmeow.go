// Package whatsmeow is the hub's WhatsApp provider: a WhatsApp Web session
// run inside the hub process by go.mau.fi/whatsmeow (MPL-2.0, issue #59).
//
// What shapes it:
//
//   - It is unofficial. It links the hub as a "companion device" of a
//     dedicated WhatsApp number, the way WhatsApp Web does, so the number
//     can be banned: use a number kept for Kergui, never a person's own.
//   - There is no webhook. Inbound messages arrive as whatsmeow events in
//     this process and are handed to the alerts module directly (OnInbound),
//     so ParseInbound refuses every HTTP request.
//   - The session lives in the hub's own Postgres (whatsmeow's sqlstore,
//     tables whatsmeow_*), so a redeploy keeps the number linked. Only one
//     process may hold it: the hub runs as one replica.
//   - Linking is a platform-admin step on the running hub (admin.go:
//     GET/POST/DELETE /api/v1/admin/whatsapp): it answers a pairing code to
//     type into WhatsApp on the phone that owns the number (Appareils
//     connectés → Connecter avec un numéro). It is not a CLI: a second
//     process would take the session over from the hub. Until linked, Send
//     answers domain.ErrNotActivated and the hub keeps alerting by push.
package whatsmeow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	wm "go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
)

// ErrNotLinked is a hub whose WhatsApp number was never linked, or was
// unlinked from the phone. It wraps domain.ErrNotActivated, which the alerts
// module already reports as "the provider is not ready" rather than as a
// transient failure.
var ErrNotLinked = fmt.Errorf("%w: WhatsApp is not linked (POST /api/v1/admin/whatsapp/link)", domain.ErrNotActivated)

// handleTimeout bounds what one inbound message may take: a « 1 » runs a
// block through the relay, which waits for the box.
const handleTimeout = 2 * time.Minute

// session is the slice of a whatsmeow client the adapter uses, so the tests
// run without WhatsApp.
type session interface {
	IsLoggedIn() bool
	SendText(ctx context.Context, to types.JID, text string) (string, error)
	PNForLID(ctx context.Context, lid types.JID) (types.JID, error)
}

// Notifier is the whatsmeow provider.
type Notifier struct {
	sess session
	log  *slog.Logger

	mu      sync.RWMutex
	deliver func(context.Context, domain.InboundMessage)
	// runCtx is Run's context: the inbound handlers it starts end with it.
	runCtx context.Context

	// client is the real session, nil in tests; container is where it is
	// stored, kept so an unlinked session can start over with a new device.
	client    *wm.Client
	container *sqlstore.Container
}

var (
	_ domain.Notifier      = (*Notifier)(nil)
	_ domain.InboundSource = (*Notifier)(nil)
)

// Name identifies the provider.
func (n *Notifier) Name() string { return "whatsmeow" }

// Attribution is empty: whatsmeow's licence (MPL-2.0) asks for no notice in
// the product, only that changes to its own files be published.
func (n *Notifier) Attribution() string { return "" }

// OnInbound wires where received messages go: the alerts module's handler.
func (n *Notifier) OnInbound(deliver func(context.Context, domain.InboundMessage)) {
	n.mu.Lock()
	n.deliver = deliver
	n.mu.Unlock()
}

// ParseInbound refuses every request: this provider has no webhook.
func (n *Notifier) ParseInbound(*http.Request) (domain.InboundMessage, error) {
	return domain.InboundMessage{}, domain.ErrNotAuthentic
}

// Send delivers one text message and returns WhatsApp's id for it, which a
// reply quotes.
func (n *Notifier) Send(ctx context.Context, to domain.Phone, msg domain.OutboundMessage) (string, error) {
	n.mu.RLock()
	sess := n.sess
	n.mu.RUnlock()
	if !sess.IsLoggedIn() {
		return "", ErrNotLinked
	}
	jid := types.NewJID(strings.TrimPrefix(string(to), "+"), types.DefaultUserServer)
	id, err := sess.SendText(ctx, jid, msg.Text)
	if err != nil {
		return "", fmt.Errorf("whatsmeow: send to %s: %w", to.Masked(), err)
	}
	return id, nil
}

// handleEvent is whatsmeow's event handler.
func (n *Notifier) handleEvent(evt any) {
	switch e := evt.(type) {
	case *events.Message:
		n.mu.RLock()
		deliver, runCtx, sess := n.deliver, n.runCtx, n.sess
		n.mu.RUnlock()
		if deliver == nil || runCtx == nil {
			return
		}
		ctx, cancel := context.WithTimeout(runCtx, handleTimeout)
		msg, err := Inbound(ctx, e, sess.PNForLID)
		if err != nil {
			cancel()
			if !errors.Is(err, domain.ErrIgnored) {
				n.log.Warn("whatsmeow: unreadable inbound message", "error", err)
			}
			return
		}
		// whatsmeow calls handlers one at a time on its own goroutine: a
		// block that waits for the box must not hold every other message.
		go func() {
			defer cancel()
			deliver(ctx, msg)
		}()
	case *events.Connected:
		n.log.Info("whatsmeow: connected to WhatsApp")
	case *events.LoggedOut:
		n.log.Error("whatsmeow: the number was unlinked from the phone; alerts go by push only until it is linked again",
			"reason", e.Reason.String(), "fix", "DELETE then POST /api/v1/admin/whatsapp/link")
	case *events.StreamReplaced:
		n.log.Error("whatsmeow: another process took over the WhatsApp session (two hub replicas?)")
	}
}

// Inbound reads a received message into what the alerts module handles. It
// returns domain.ErrIgnored for what is not a person writing to the number
// one to one: our own messages, groups, status updates, broadcasts and
// messages without text.
func Inbound(ctx context.Context, e *events.Message, pnForLID func(context.Context, types.JID) (types.JID, error)) (domain.InboundMessage, error) {
	info := e.Info
	if info.IsFromMe || info.IsGroup || info.Chat.Server == types.BroadcastServer || info.IsIncomingBroadcast() {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	text, quoted := textOf(e.Message)
	if strings.TrimSpace(text) == "" {
		return domain.InboundMessage{}, domain.ErrIgnored
	}
	sender, err := phoneJID(ctx, info.MessageSource, pnForLID)
	if err != nil {
		return domain.InboundMessage{}, err
	}
	from, err := domain.ParsePhone("+" + sender.User)
	if err != nil {
		return domain.InboundMessage{}, fmt.Errorf("whatsmeow: sender %q: %w", sender.User, err)
	}
	return domain.InboundMessage{
		From: from, Text: text, QuotedProviderID: quoted,
		ProviderID: info.ID, At: info.Timestamp,
	}, nil
}

// phoneJID is the sender's phone-number address. WhatsApp increasingly
// addresses people by an opaque LID; the phone number then travels as the
// alternative address, or is found in the LID map whatsmeow keeps.
func phoneJID(ctx context.Context, src types.MessageSource, pnForLID func(context.Context, types.JID) (types.JID, error)) (types.JID, error) {
	for _, j := range []types.JID{src.Sender, src.SenderAlt} {
		if j.Server == types.DefaultUserServer && j.User != "" {
			return j.ToNonAD(), nil
		}
	}
	if src.Sender.Server == types.HiddenUserServer && pnForLID != nil {
		pn, err := pnForLID(ctx, src.Sender.ToNonAD())
		if err == nil && pn.User != "" {
			return pn.ToNonAD(), nil
		}
	}
	return types.JID{}, fmt.Errorf("%w: no phone number for sender %s", domain.ErrIgnored, src.Sender)
}

// textOf is the message's text and, for a reply, the id it quotes.
func textOf(m *waE2E.Message) (text, quoted string) {
	if m == nil {
		return "", ""
	}
	if c := m.GetConversation(); c != "" {
		return c, ""
	}
	if x := m.GetExtendedTextMessage(); x != nil {
		return x.GetText(), x.GetContextInfo().GetStanzaID()
	}
	return "", ""
}

// --- the real session --------------------------------------------------------

// client adapts *whatsmeow.Client to session.
type client struct{ c *wm.Client }

func (c client) IsLoggedIn() bool { return c.c.IsLoggedIn() }

func (c client) SendText(ctx context.Context, to types.JID, text string) (string, error) {
	resp, err := c.c.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

func (c client) PNForLID(ctx context.Context, lid types.JID) (types.JID, error) {
	return c.c.Store.LIDs.GetPNForLID(ctx, lid)
}

// Open loads (or creates) the session stored in the hub's database. It does
// not connect: Run does.
func Open(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) (*Notifier, error) {
	container := sqlstore.NewWithDB(sqlDB(pool), "postgres", wmLog{log})
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("whatsmeow: session tables: %w", err)
	}
	d, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("whatsmeow: session: %w", err)
	}
	n := &Notifier{log: log, container: container}
	n.use(d)
	return n, nil
}

// use makes d the session.
func (n *Notifier) use(d *store.Device) {
	c := wm.NewClient(d, wmLog{n.log})
	c.AddEventHandler(n.handleEvent)
	n.mu.Lock()
	n.client, n.sess = c, client{c}
	n.mu.Unlock()
}

var (
	sqlOnce sync.Once
	sqlConn *sql.DB
)

// sqlDB is a database/sql view of the hub's pool, which sqlstore wants.
func sqlDB(pool *pgxpool.Pool) *sql.DB {
	sqlOnce.Do(func() { sqlConn = stdlib.OpenDBFromPool(pool) })
	return sqlConn
}

// Run keeps the session connected until ctx ends. whatsmeow reconnects on
// its own after a network drop. An unlinked hub only logs how to link it.
func (n *Notifier) Run(ctx context.Context) error {
	n.mu.Lock()
	n.runCtx = ctx
	n.mu.Unlock()
	n.mu.RLock()
	c := n.client
	n.mu.RUnlock()
	if c.Store.ID == nil {
		n.log.Warn("whatsmeow: no WhatsApp number linked; alerts go by push only",
			"fix", "POST /api/v1/admin/whatsapp/link (platform admin)")
		<-ctx.Done()
		n.disconnect()
		return nil
	}
	if err := c.Connect(); err != nil {
		// Not fatal to the hub: whatsmeow keeps retrying once connected
		// once, and a hub without WhatsApp still alerts by push.
		n.log.Error("whatsmeow: cannot connect to WhatsApp", "error", err)
	}
	<-ctx.Done()
	n.disconnect()
	return nil
}

func (n *Notifier) disconnect() {
	n.mu.RLock()
	c := n.client
	n.mu.RUnlock()
	c.Disconnect()
}

// Status is what the admin route shows.
type Status struct {
	// Linked is a number paired with the hub; Number is that number.
	Linked    bool   `json:"linked"`
	Number    string `json:"number,omitempty"`
	Connected bool   `json:"connected"`
}

// Status reports the session.
func (n *Notifier) Status() Status {
	n.mu.RLock()
	c := n.client
	n.mu.RUnlock()
	st := Status{Connected: c.IsConnected() && c.IsLoggedIn()}
	if id := c.Store.ID; id != nil {
		st.Linked, st.Number = true, "+"+id.User
	}
	return st
}

// ErrAlreadyLinked is a link asked for while a number is linked.
var ErrAlreadyLinked = errors.New("whatsmeow: a number is already linked; unlink it first")

// Link starts pairing the running hub with a WhatsApp number and returns the
// code to type on that phone (WhatsApp → Appareils connectés → Connecter un
// appareil → Connecter avec un numéro de téléphone). The pairing completes
// in the background; Status shows when it has.
//
// It runs inside the hub that holds the session: a second process linking
// at the same time would take the session over.
func (n *Notifier) Link(ctx context.Context, number domain.Phone) (string, error) {
	n.mu.RLock()
	c := n.client
	n.mu.RUnlock()
	if c.Store.ID != nil {
		return "", ErrAlreadyLinked
	}
	if !c.IsConnected() {
		if err := c.Connect(); err != nil {
			return "", fmt.Errorf("whatsmeow: connect: %w", err)
		}
	}
	code, err := c.PairPhone(ctx, strings.TrimPrefix(string(number), "+"), true, wm.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return "", fmt.Errorf("whatsmeow: pairing code: %w", err)
	}
	n.log.Info("whatsmeow: pairing code issued, waiting for the phone", "number", number.Masked())
	return code, nil
}

// Unlink logs the hub out of WhatsApp and forgets the session, so another
// number can be linked.
func (n *Notifier) Unlink(ctx context.Context) error {
	n.mu.RLock()
	c := n.client
	n.mu.RUnlock()
	if c.Store.ID != nil {
		if err := c.Logout(ctx); err != nil {
			// Logged out from the phone already: drop what is stored.
			n.log.Warn("whatsmeow: logout", "error", err)
			if err := c.Store.Delete(ctx); err != nil {
				return fmt.Errorf("whatsmeow: forget the session: %w", err)
			}
		}
	}
	c.Disconnect()
	n.use(n.container.NewDevice())
	return nil
}

// wmLog sends whatsmeow's logs to the hub's: warnings and errors only, its
// info is chatty.
type wmLog struct{ l *slog.Logger }

func (w wmLog) Errorf(msg string, args ...any) { w.l.Error("whatsmeow: " + fmt.Sprintf(msg, args...)) }
func (w wmLog) Warnf(msg string, args ...any)  { w.l.Warn("whatsmeow: " + fmt.Sprintf(msg, args...)) }
func (w wmLog) Infof(string, ...any)           {}
func (w wmLog) Debugf(string, ...any)          {}
func (w wmLog) Sub(string) waLog.Logger        { return w }
