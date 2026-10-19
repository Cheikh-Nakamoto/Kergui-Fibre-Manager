// Package replyblock is WhatsApp's "1 = block" (RFC-005): it turns a
// household's reply into one signed command on the box.
//
// It is the one place where the hub itself signs something, so it is also
// the place where the hub is kept narrow. The key it signs with is derived
// per household (internal/hub/hubkey) and only acts because the owner
// delegated it hub_block in a bundle they signed; the relay re-checks every
// one of those conditions and adds its own — the device has to be on the
// network right now. Here the hub checks them too, before spending a
// household's rate limit on a command the relay would refuse:
//
//   - the number is verified and belongs to an owner or admin of the
//     household the alert was sent to;
//   - the number answered is a device of that alert, resolved from the
//     alert, never from the text of the message;
//   - the household's bundle delegates hub_block to this hub's key, has not
//     revoked it, and is not owner-only;
//   - the device is not protected, which includes the household's relays;
//   - the household is within its rate limit.
//
// A reply is acted on once: the claim on (alert, device number) is what
// makes a provider's retry harmless.
package replyblock

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"
	"github.com/jackc/pgx/v5/pgxpool"

	agentsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/agents/domain"
	alertsdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	alertsusecase "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/hubkey"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/relay"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// Migrations holds the SQL this module owns.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// ActionWhatsAppBlock is recorded for every reply acted on, so the owner
// can see in the app that a device was blocked from a message and by whom.
const ActionWhatsAppBlock = auditdomain.AuditAction("WHATSAPP_BLOCK_REQUESTED")

// Defaults. The limit and the window are the relay's own
// (trust.DefaultHubBlockLimit), kept in step on purpose: the hub refusing
// first only saves a round trip, it never decides.
const (
	DefaultWait = 15 * time.Second
	// CommandTTL is the lifetime of the signed command. The relay caps it
	// at trust.MaxCommandTTL anyway.
	CommandTTL = trust.MaxCommandTTL
)

// TrustReader is the slice of the agents module this one needs.
type TrustReader interface {
	GetBundle(ctx context.Context, tenantID string) (agentsdomain.TrustBundle, error)
}

// AdminLookup answers whether a user may act on a household's box. It is a
// port so this module never queries the kernel's membership tables itself.
type AdminLookup func(ctx context.Context, tenantID, userID string) (bool, error)

// Logger is the slice of a logger this module uses.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// Config captures what the module needs.
type Config struct {
	Pool   *pgxpool.Pool
	Guard  *tenantaccess.Guard
	Alerts alertsdomain.Repository
	Trust  TrustReader
	// Commands runs the signed command and verifies the relay's answer.
	Commands *relay.Commands
	// Keys derives the household's hub key; nil turns the whole path off,
	// which is what a deployment with no seed gets.
	Keys    *hubkey.Deriver
	IsAdmin AdminLookup
	Audit   audit.EventLogger
	Logger  *slog.Logger
	Now     func() time.Time
	// Wait is how long to wait for the relay before answering "not
	// confirmed"; Limit and Window are the rate limit.
	Wait   time.Duration
	Limit  int
	Window time.Duration
}

// Service answers a household's reply.
type Service struct {
	repo    *Repo
	cfg     Config
	now     func() time.Time
	log     Logger
	wait    time.Duration
	limit   int
	window  time.Duration
	enabled bool
}

// New wires the service.
func New(cfg Config) *Service {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	wait := cfg.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = trust.DefaultHubBlockLimit
	}
	window := cfg.Window
	if window <= 0 {
		window = trust.DefaultHubBlockWindow
	}
	var log Logger
	if cfg.Logger != nil {
		log = cfg.Logger
	}
	return &Service{
		repo: NewRepo(cfg.Pool), cfg: cfg, now: now, log: log,
		wait: wait, limit: limit, window: window,
		enabled: cfg.Keys != nil && cfg.Commands != nil,
	}
}

// Reply acts on one inbound reply and returns what to answer in the
// conversation. The error is for the hub's log; the household gets the
// text, which never names a key, a MAC it did not already know, or a
// household it is not part of.
func (s *Service) Reply(ctx context.Context, msg alertsdomain.InboundMessage, intent alertsdomain.Intent) (string, error) {
	if !s.enabled {
		return "Le blocage par message n'est pas activé sur ce hub. " +
			"Ouvrez l'application Kergui pour bloquer cet appareil.", hubkey.ErrNoSeed
	}
	now := s.now().UTC()
	alert, err := s.cfg.Alerts.AlertForReply(ctx, msg.From, msg.QuotedProviderID, now)
	if err != nil {
		if errors.Is(err, alertsdomain.ErrNoAlert) {
			return "Je ne vois pas d'alerte récente à laquelle répondre. " +
				"Ouvrez l'application Kergui pour voir les appareils connectés.", err
		}
		return "", err
	}
	member, err := s.member(ctx, msg.From, alert.TenantID)
	if err != nil {
		return "Ce numéro n'est pas autorisé à bloquer un appareil de ce foyer.", err
	}

	answers := make([]string, 0, len(intent.Choices))
	for _, no := range intent.Choices {
		device, ok := alert.Device(no)
		if !ok {
			answers = append(answers, fmt.Sprintf("❌ Le numéro %d ne correspond à aucun appareil de cette alerte.", no))
			continue
		}
		answers = append(answers, s.blockOne(ctx, alert, device, member, now))
	}
	return strings.Join(answers, "\n"), nil
}

// member resolves which member of the household this number belongs to,
// and whether they may act on the box.
func (s *Service) member(ctx context.Context, phone alertsdomain.Phone, tenantID string) (string, error) {
	optIns, err := s.cfg.Alerts.OptInsByPhone(ctx, phone)
	if err != nil {
		return "", err
	}
	for _, o := range optIns {
		if o.TenantID != tenantID || !o.Active() {
			continue
		}
		admin, err := s.cfg.IsAdmin(ctx, tenantID, o.UserID)
		if err != nil {
			return "", err
		}
		if !admin {
			// A viewer may be told what joined the network and still not
			// get to cut it off.
			return "", fmt.Errorf("%w: %s is not an admin of this household", alertsdomain.ErrUnknownPhone, phone.Masked())
		}
		return o.UserID, nil
	}
	return "", fmt.Errorf("%w: %s", alertsdomain.ErrUnknownPhone, phone.Masked())
}

// blockOne blocks one device of an alert and returns the sentence to answer.
func (s *Service) blockOne(ctx context.Context, alert alertsdomain.Alert, device alertsdomain.AlertDevice, userID string, now time.Time) string {
	key, err := s.cfg.Keys.For(alert.TenantID)
	if err != nil {
		s.warn("replyblock: cannot derive the hub key", alert.TenantID, err)
		return alertsusecase.ComposeRefused(device, "le blocage par message n'est pas disponible")
	}
	if reason, ok := s.allowed(ctx, alert.TenantID, device, key.KeyID()); !ok {
		return alertsusecase.ComposeRefused(device, reason)
	}
	// Within the rate limit, and only then claimed: a refused reply must
	// not spend the household's budget, and a claimed one must not be
	// acted on twice.
	used, err := s.repo.CountSince(ctx, alert.TenantID, now.Add(-s.window))
	if err != nil {
		s.warn("replyblock: cannot read the rate limit", alert.TenantID, err)
		return alertsusecase.ComposeRefused(device, "une erreur interne est survenue")
	}
	if used >= s.limit {
		return alertsusecase.ComposeRefused(device,
			"trop de blocages par message cette heure-ci. Ouvrez l'application Kergui pour continuer")
	}
	claimed, err := s.repo.Claim(ctx, Action{
		TenantID: alert.TenantID, AlertID: alert.ID, ItemNo: device.No,
		MAC: device.MAC, UserID: userID, Status: StatusQueued, At: now,
	})
	if err != nil {
		s.warn("replyblock: cannot record the reply", alert.TenantID, err)
		return alertsusecase.ComposeRefused(device, "une erreur interne est survenue")
	}
	if !claimed {
		// The provider delivered the same reply twice, or the household
		// answered twice: say what is true rather than block again.
		return "✅ " + deviceName(device) + " était déjà bloqué."
	}

	path := "/api/devices/" + device.MAC + "/block"
	signed, err := trust.NewCommand(key, alert.TenantID, http.MethodPost, path, nil, now, CommandTTL)
	if err != nil {
		s.fail(ctx, alert, device, userID, now, "signature")
		return alertsusecase.ComposeRefused(device, "une erreur interne est survenue")
	}
	s.Audit(ctx, alert, device, userID)

	out, err := s.cfg.Commands.Run(ctx, relay.RunParams{
		TenantID: alert.TenantID, IssuerUserID: userID, Signed: signed,
		WantMethod: http.MethodPost, WantPath: path, Wait: s.wait,
	})
	switch {
	case err != nil:
		s.warn("replyblock: the command could not be run", alert.TenantID, err)
		s.fail(ctx, alert, device, userID, now, "queue")
		return alertsusecase.ComposeRefused(device, "le hub n'a pas pu transmettre la demande")
	case !out.Answered:
		s.record(ctx, alert, device, userID, now, StatusPending, out.CommandID, "")
		return "⏳ " + deviceName(device) + " : demande envoyée, en attente du routeur. " +
			"Vous verrez le résultat dans l'application Kergui."
	case out.Status >= 300:
		s.record(ctx, alert, device, userID, now, StatusRefused, out.CommandID, refusalReason(out.Body))
		return alertsusecase.ComposeRefused(device, household(out))
	default:
		s.record(ctx, alert, device, userID, now, StatusDone, out.CommandID, "")
		return alertsusecase.ComposeBlocked(device)
	}
}

// allowed applies the hub's own reading of the household's bundle. The
// relay's reading is the one that decides; this one saves a round trip and
// gives the household a sentence instead of a silence.
func (s *Service) allowed(ctx context.Context, tenantID string, device alertsdomain.AlertDevice, keyID string) (string, bool) {
	stored, err := s.cfg.Trust.GetBundle(ctx, tenantID)
	if err != nil {
		return "ce foyer n'a pas encore de clés de confiance", false
	}
	signed := trust.Signed[trust.Bundle]{Payload: stored.Payload, Sig: stored.Sig}
	bundle, err := signed.Unverified()
	if err != nil {
		return "les clés de confiance de ce foyer sont illisibles", false
	}
	if bundle.OwnerOnly {
		return "ce foyer est réservé à son propriétaire", false
	}
	for _, r := range bundle.Revoked {
		if r == keyID {
			return "le blocage par message a été désactivé pour ce foyer", false
		}
	}
	delegated := false
	for _, d := range bundle.Delegations {
		if d.KeyID() == keyID && d.Scope == trust.ScopeHubBlock {
			if !d.ExpiresAt.IsZero() && !s.now().Before(d.ExpiresAt) {
				return "l'autorisation de blocage par message a expiré", false
			}
			delegated = true
		}
	}
	if !delegated {
		return "le blocage par message n'est pas autorisé pour ce foyer", false
	}
	if bundle.IsProtected(device.MAC) {
		// Covers the owner's pinned devices and the household's relays.
		return "cet appareil est protégé", false
	}
	return "", true
}

// household turns the relay's refusal into something a person can act on.
func household(out relay.Outcome) string {
	switch reason := refusalReason(out.Body); {
	case strings.Contains(reason, "protected"):
		return "cet appareil est protégé"
	case strings.Contains(reason, "not on the network"):
		return "cet appareil n'est plus connecté"
	case strings.Contains(reason, "rate limit"):
		return "trop de blocages par message cette heure-ci"
	case reason == "":
		return "le routeur a refusé la demande"
	default:
		return "le routeur a refusé la demande"
	}
}

// refusalReason reads the relay's refusal code out of its signed body.
func refusalReason(body json.RawMessage) string {
	var out struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &out) != nil {
		return ""
	}
	return out.Error
}

func deviceName(d alertsdomain.AlertDevice) string {
	switch {
	case d.Hostname != "":
		return d.Hostname
	case d.IP != "":
		return "l'appareil " + d.IP
	default:
		return "l'appareil " + d.MAC
	}
}

// Audit records that a reply asked for a block. It is recorded when the
// command is signed, not when it succeeds: the owner's question is "who
// asked for this", and the outcome follows in the command's own audit row.
func (s *Service) Audit(ctx context.Context, alert alertsdomain.Alert, device alertsdomain.AlertDevice, userID string) {
	if s.cfg.Audit == nil {
		return
	}
	s.cfg.Audit.Execute(ctx, audit.LogEventParams{
		TenantID: alert.TenantID, ActorID: userID, Action: ActionWhatsAppBlock,
		TargetType: "device",
		Metadata: map[string]any{
			"channel": "whatsapp", "alert_id": alert.ID,
			"item_no": device.No, "mac": device.MAC,
		},
	})
}

func (s *Service) record(ctx context.Context, alert alertsdomain.Alert, device alertsdomain.AlertDevice, userID string, now time.Time, status Status, cmdID, reason string) {
	if err := s.repo.Finish(ctx, alert.TenantID, alert.ID, device.No, status, cmdID, reason, now); err != nil {
		s.warn("replyblock: cannot record the outcome", alert.TenantID, err)
	}
}

func (s *Service) fail(ctx context.Context, alert alertsdomain.Alert, device alertsdomain.AlertDevice, userID string, now time.Time, reason string) {
	s.record(ctx, alert, device, userID, now, StatusFailed, "", reason)
}

func (s *Service) warn(msg, tenantID string, err error) {
	if s.log != nil {
		s.log.Warn(msg, "tenant_id", tenantID, "error", err)
	}
}
