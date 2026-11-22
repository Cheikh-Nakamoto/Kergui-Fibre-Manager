// Package identity makes the phone number verified over WhatsApp (a
// licensing account, RFC-006) the hub identity (issue #41). Signing an
// account in creates, the first time, a kernel user and the household
// tenant it owns, then issues a kernel access token and a rotating refresh
// token for /api/v1/tenants/{id}/...
//
// The kernel stays unforked: this package composes its public repositories
// and its token signer the way its own SignupOwner use case does, minus the
// e-mail and the password. The kernel's e-mail signup stays for the CLI and
// the tests; the app never uses it.
package identity

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"
	authdomain "github.com/agile-factory/kernel/platform/auth/domain"
	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"
	tenantdomain "github.com/agile-factory/kernel/platform/tenant/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Migrations holds the SQL this module owns. It references the kernel's
// users and tenants and the licensing accounts, so it runs after them.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// EmailDomain is the domain of the placeholder e-mail a phone user is
// given: the kernel's users table requires a unique, valid address. The
// .invalid TLD (RFC 2606) can never receive mail, so the notification
// module has nowhere to send anything.
const EmailDomain = "phone.kergui.invalid"

// defaultRefreshTTL is the kernel's own default (auth.New).
const defaultRefreshTTL = 30 * 24 * time.Hour

// ErrUserInactive is a household whose kernel user was suspended or
// deleted: it cannot be signed in, whatever code was typed.
var ErrUserInactive = errors.New("identity: the household's user is not active")

// TokenGenerator is the kernel's access token signer (auth.Module.JWTService).
type TokenGenerator interface {
	GenerateToken(user *authdomain.User) (string, error)
}

// Config are the kernel pieces the module composes.
type Config struct {
	Pool          *pgxpool.Pool
	Users         authdomain.UserRepository
	Tenants       tenantdomain.TenantRepository
	Settings      tenantdomain.TenantSettingsRepository
	Members       memberdomain.MembershipRepository
	RefreshTokens authdomain.RefreshTokenRepository
	Tokens        TokenGenerator
	// RefreshTTL must be the auth module's, so a pair issued here lives as
	// long as one rotated at /api/v1/auth/refresh. 0 is the kernel default.
	RefreshTTL  time.Duration
	AuditLogger audit.EventLogger // optional, best effort as in the kernel

	// Household members (issue #42).
	Guard   *tenantaccess.Guard
	Sender  Sender  // the WhatsApp invite
	Revoker Revoker // the kernel's membership revocation
	// OnRemoved runs, in the removal's transaction, once a member is out:
	// the hub stops their WhatsApp alerts for the household.
	OnRemoved func(ctx context.Context, tenantID, userID string) error
	Now       func() time.Time
}

// Module signs licensing accounts in to their household.
type Module struct{ cfg Config }

// New wires the module.
func New(cfg Config) *Module {
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = defaultRefreshTTL
	}
	return &Module{cfg: cfg}
}

var _ usecase.Identity = (*Module)(nil)

// SignIn implements usecase.Identity: it finds or creates the account's
// user and household, then issues a token pair scoped to that household.
func (m *Module) SignIn(ctx context.Context, a usecase.Account) (usecase.HubSession, error) {
	if a.ID == "" {
		return usecase.HubSession{}, errors.New("identity: an account id is required")
	}
	var (
		user     *authdomain.User
		tenantID string
		created  bool
	)
	err := txctx.ExecInTx(ctx, m.cfg.Pool, func(ctx context.Context) error {
		db := txctx.DB(ctx, m.cfg.Pool)
		// Two phones verifying the same number at once must not create two
		// households: serialise on the account.
		if _, err := db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('kergui.identity:' || $1, 0))`, string(a.ID)); err != nil {
			return err
		}
		var userID string
		err := db.QueryRow(ctx, `SELECT user_id, tenant_id FROM hub_identity_households WHERE account_id = $1`,
			string(a.ID)).Scan(&userID, &tenantID)
		switch {
		case err == nil:
			user, err = m.cfg.Users.FindByID(ctx, userID)
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		user, tenantID, err = m.createHousehold(ctx, a)
		if err != nil {
			return err
		}
		created = true
		_, err = db.Exec(ctx, `INSERT INTO hub_identity_households (account_id, user_id, tenant_id) VALUES ($1, $2, $3)`,
			string(a.ID), user.ID, tenantID)
		return err
	})
	if err == nil && a.Phone != "" {
		// The number's open invites (issue #42) are accepted now, and the
		// session goes to the household it most recently joined.
		err = txctx.ExecInTx(ctx, m.cfg.Pool, func(ctx context.Context) error {
			if err := m.acceptInvites(ctx, user.ID, a.Phone); err != nil {
				return err
			}
			joined, err := m.joinedHousehold(ctx, user.ID, tenantID)
			if err == nil && joined != "" {
				tenantID = joined
			}
			return err
		})
	}
	if err != nil {
		return usecase.HubSession{}, fmt.Errorf("identity: %w", err)
	}
	if !user.IsActive() {
		return usecase.HubSession{}, ErrUserInactive
	}

	// The pair is issued outside the transaction, as the kernel's signup
	// does: the user exists, and a retry only mints another random token.
	access, err := m.cfg.Tokens.GenerateToken(user)
	if err != nil {
		return usecase.HubSession{}, fmt.Errorf("identity: access token: %w", err)
	}
	raw, refresh, err := authdomain.NewRefreshToken(uuid.NewString(), user.ID, tenantID, m.cfg.RefreshTTL)
	if err != nil {
		return usecase.HubSession{}, fmt.Errorf("identity: refresh token: %w", err)
	}
	if err := m.cfg.RefreshTokens.Save(ctx, refresh); err != nil {
		return usecase.HubSession{}, fmt.Errorf("identity: refresh token: %w", err)
	}
	m.audit(ctx, a, user.ID, tenantID, created)
	return usecase.HubSession{UserID: user.ID, TenantID: tenantID, AccessToken: access, RefreshToken: raw}, nil
}

// createHousehold creates the verified user, the household tenant with its
// default settings, and the active owner membership, inside the caller's
// transaction.
func (m *Module) createHousehold(ctx context.Context, a usecase.Account) (*authdomain.User, string, error) {
	email, err := authdomain.NewEmail(Email(a.ID))
	if err != nil {
		return nil, "", err
	}
	unusable, err := unusablePassword()
	if err != nil {
		return nil, "", err
	}
	// Verified: the WhatsApp code proved the number, as an invite token
	// proves an address. The platform role is the signup's.
	user := authdomain.NewVerifiedUser(uuid.NewString(), email, unusable, authdomain.ROLE_VIEWER)
	if err := m.cfg.Users.Save(ctx, user); err != nil {
		return nil, "", err
	}
	tenant, err := tenantdomain.NewTenant(uuid.NewString(), "Foyer "+a.Phone.Masked(), Slug(a.ID))
	if err != nil {
		return nil, "", err
	}
	if err := m.cfg.Tenants.Save(ctx, tenant); err != nil {
		return nil, "", err
	}
	if err := m.cfg.Settings.Save(ctx, tenantdomain.DefaultSettings(tenant.ID)); err != nil {
		return nil, "", err
	}
	owner := memberdomain.NewSelfMembership(uuid.NewString(), user.ID, tenant.ID, memberdomain.MEMBER_ROLE_OWNER)
	if err := m.cfg.Members.Save(ctx, owner); err != nil {
		return nil, "", err
	}
	return user, tenant.ID, nil
}

func (m *Module) audit(ctx context.Context, a usecase.Account, userID, tenantID string, created bool) {
	if m.cfg.AuditLogger == nil {
		return
	}
	action := auditdomain.ACTION_USER_LOGGED_IN
	if created {
		action = auditdomain.ACTION_USER_REGISTERED
	}
	m.cfg.AuditLogger.Execute(ctx, audit.LogEventParams{
		TenantID: tenantID, ActorID: userID, Action: action, TargetType: "user", TargetID: userID,
		Metadata: map[string]any{"flow": "whatsapp_otp", "account": string(a.ID), "phone": a.Phone.Masked()},
	})
}

// Email is the placeholder address of an account's kernel user. It is keyed
// on the account id, not the number, so a number can change without a new
// user.
func Email(id licensingdomain.AccountID) string { return "acc-" + string(id) + "@" + EmailDomain }

// Slug is the household's tenant slug, unique because the account id is.
func Slug(id licensingdomain.AccountID) string { return "foyer-" + string(id) }

// unusablePassword is what the kernel's users table gets in place of a
// bcrypt hash: no password compares equal to it, so the e-mail login can
// never open a phone household.
func unusablePassword() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "!phone:" + base64.RawURLEncoding.EncodeToString(b[:]), nil
}
