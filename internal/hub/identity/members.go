package identity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"
	memberdomain "github.com/agile-factory/kernel/platform/membership/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	alerts "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/alerts/domain"
	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/tenantaccess"
)

// Household members (issue #42). The owner invites a number with a role;
// the invite goes out on WhatsApp and is accepted when that number signs in
// (SignIn). The membership is the kernel's: the owner lists, changes and
// removes members, and removing one ends their sessions on the household.

// Role is a member's role as the app names it.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// kernelRole is the kernel's name for an invitable role.
func (r Role) kernelRole() (memberdomain.MemberRole, bool) {
	switch r {
	case RoleAdmin:
		return memberdomain.MEMBER_ROLE_ADMIN, true
	case RoleViewer:
		return memberdomain.MEMBER_ROLE_VIEWER, true
	}
	return "", false
}

// roleOf names a kernel role for the app. The kernel's ROLE_AGENT is not
// used by the hub; it reads as a viewer, the narrowest role.
func roleOf(r memberdomain.MemberRole) Role {
	switch r {
	case memberdomain.MEMBER_ROLE_OWNER:
		return RoleOwner
	case memberdomain.MEMBER_ROLE_ADMIN:
		return RoleAdmin
	}
	return RoleViewer
}

// Invite parameters.
const (
	// InviteTTL is the kernel's own invite lifetime.
	InviteTTL = 7 * 24 * time.Hour
	// InvitesPerDay caps the invites a household sends in a day, so the
	// route cannot be used to send WhatsApp messages to strangers.
	InvitesPerDay = 20
)

// Errors the members routes answer with. Each maps to its own code.
var (
	ErrOwnerOnly       = errors.New("identity: only the household's owner manages its members")
	ErrInvalidRole     = errors.New("identity: the role must be admin or viewer")
	ErrAlreadyMember   = errors.New("identity: this number is already a member of the household")
	ErrOwnerUntouched  = errors.New("identity: the owner's membership cannot be changed or removed")
	ErrNoSuchMember    = errors.New("identity: no such member")
	ErrNoSuchInvite    = errors.New("identity: no such invite")
	ErrInviteLimit     = errors.New("identity: too many invites today")
	ErrMessageNotSent  = errors.New("identity: the WhatsApp invite could not be sent")
	ErrMembersDisabled = errors.New("identity: members are not wired on this hub")
)

// Sender delivers the WhatsApp invite: the hub's messaging provider.
type Sender interface {
	Send(ctx context.Context, to alerts.Phone, msg alerts.OutboundMessage) (string, error)
}

// Revoker is the kernel's membership revocation (membership.Module.RevokeUC):
// it checks the revoker, refuses the owner and audits.
type Revoker interface {
	Execute(ctx context.Context, membershipID, revokerID string) (*memberdomain.Membership, error)
}

// Member is one active member of a household.
type Member struct {
	MembershipID string
	UserID       string
	Role         Role
	// Phone is the number the member signs in with; empty for a member who
	// joined through the kernel's e-mail flow.
	Phone    licensingdomain.Phone
	JoinedAt *time.Time
}

// Invite is an invite the owner sent and nobody has accepted yet.
type Invite struct {
	ID        string
	TenantID  string
	Phone     licensingdomain.Phone
	Role      Role
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Household is what the Household screen shows.
type Household struct {
	TenantID string
	Name     string
	// You is the caller's own membership.
	You     Member
	Members []Member
	// Invites are listed to the owner only.
	Invites []Invite
}

func (m *Module) now() time.Time {
	if m.cfg.Now != nil {
		return m.cfg.Now()
	}
	return time.Now()
}

// ListMembers lists a household's active members, and its pending invites
// when the caller owns it. Every member may list: a household knows who is
// in it. A viewer sees the others' numbers masked.
func (m *Module) ListMembers(ctx context.Context, caller tenantaccess.Caller) (Household, error) {
	tenant, err := m.cfg.Tenants.FindByID(ctx, caller.TenantID)
	if err != nil {
		return Household{}, fmt.Errorf("identity: household: %w", err)
	}
	all, err := m.cfg.Members.ListByTenant(ctx, caller.TenantID)
	if err != nil {
		return Household{}, fmt.Errorf("identity: members: %w", err)
	}
	var active []*memberdomain.Membership
	ids := make([]string, 0, len(all))
	for _, ms := range all {
		if ms.IsActive() {
			active = append(active, ms)
			ids = append(ids, ms.UserID)
		}
	}
	phones, err := m.phonesOf(ctx, ids)
	if err != nil {
		return Household{}, err
	}
	full := caller.IsAdmin()
	out := Household{TenantID: tenant.ID, Name: tenant.Name}
	for _, ms := range active {
		mem := Member{MembershipID: ms.ID, UserID: ms.UserID, Role: roleOf(ms.Role), Phone: phones[ms.UserID], JoinedAt: ms.JoinedAt}
		if ms.UserID == caller.UserID {
			out.You = mem
		}
		if !full && ms.UserID != caller.UserID && mem.Phone != "" {
			mem.Phone = licensingdomain.Phone(mem.Phone.Masked())
		}
		out.Members = append(out.Members, mem)
	}
	// The owner first, then by arrival.
	sort.SliceStable(out.Members, func(i, j int) bool {
		a, b := out.Members[i], out.Members[j]
		if (a.Role == RoleOwner) != (b.Role == RoleOwner) {
			return a.Role == RoleOwner
		}
		return joined(a).Before(joined(b))
	})
	if caller.IsOwner() {
		out.Invites, err = m.openInvites(ctx, caller.TenantID)
		if err != nil {
			return Household{}, err
		}
	}
	return out, nil
}

func joined(m Member) time.Time {
	if m.JoinedAt == nil {
		return time.Time{}
	}
	return *m.JoinedAt
}

// InviteMember invites a number into the household with a role and tells
// it on WhatsApp. Inviting a number again replaces its open invite (and
// sends the message again).
func (m *Module) InviteMember(ctx context.Context, caller tenantaccess.Caller, rawPhone string, role Role) (Invite, error) {
	if !caller.IsOwner() {
		return Invite{}, ErrOwnerOnly
	}
	kernelRole, ok := role.kernelRole()
	if !ok {
		return Invite{}, ErrInvalidRole
	}
	phone, err := licensingdomain.ParsePhone(rawPhone)
	if err != nil {
		return Invite{}, err
	}
	if m.cfg.Sender == nil {
		return Invite{}, ErrMembersDisabled
	}
	tenant, err := m.cfg.Tenants.FindByID(ctx, caller.TenantID)
	if err != nil {
		return Invite{}, fmt.Errorf("identity: household: %w", err)
	}
	inviterPhone, err := m.phoneOf(ctx, caller.UserID)
	if err != nil {
		return Invite{}, err
	}

	now := m.now().UTC()
	inv := Invite{
		ID: uuid.NewString(), TenantID: caller.TenantID, Phone: phone, Role: role,
		CreatedAt: now, ExpiresAt: now.Add(InviteTTL),
	}
	err = txctx.ExecInTx(ctx, m.cfg.Pool, func(ctx context.Context) error {
		db := txctx.DB(ctx, m.cfg.Pool)
		// Serialise the household's invites, for the daily cap.
		if _, err := db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('kergui.invites:' || $1, 0))`, caller.TenantID); err != nil {
			return err
		}
		if member, err := m.isMember(ctx, phone, caller.TenantID); err != nil {
			return err
		} else if member {
			return ErrAlreadyMember
		}
		var sent int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM hub_identity_invites WHERE tenant_id = $1 AND created_at > $2`,
			caller.TenantID, now.Add(-24*time.Hour)).Scan(&sent); err != nil {
			return err
		}
		if sent >= InvitesPerDay {
			return ErrInviteLimit
		}
		if _, err := db.Exec(ctx, `UPDATE hub_identity_invites SET revoked_at = $3
			WHERE tenant_id = $1 AND phone = $2 AND accepted_at IS NULL AND revoked_at IS NULL`,
			caller.TenantID, string(phone), now); err != nil {
			return err
		}
		_, err := db.Exec(ctx, `INSERT INTO hub_identity_invites (id, tenant_id, phone, role, invited_by, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			inv.ID, inv.TenantID, string(phone), string(kernelRole), caller.UserID, inv.CreatedAt, inv.ExpiresAt)
		return err
	})
	if err != nil {
		return Invite{}, fmt.Errorf("identity: invite: %w", err)
	}

	// The message goes out after the commit; an invite nobody was told
	// about is withdrawn rather than left waiting.
	text := InviteText(inviterPhone, tenant.Name, role, inv.ExpiresAt)
	if _, err := m.cfg.Sender.Send(ctx, phone, alerts.OutboundMessage{Text: text}); err != nil {
		if _, uerr := m.cfg.Pool.Exec(ctx, `UPDATE hub_identity_invites SET revoked_at = now() WHERE id = $1`, inv.ID); uerr != nil {
			return Invite{}, fmt.Errorf("identity: withdraw an unsent invite: %w", uerr)
		}
		return Invite{}, fmt.Errorf("%w: %v", ErrMessageNotSent, err)
	}
	m.auditEvent(ctx, caller, auditdomain.ACTION_MEMBER_INVITED, "invite", inv.ID,
		map[string]any{"phone": phone.Masked(), "role": string(kernelRole), "channel": "whatsapp"})
	return inv, nil
}

// InviteText is the WhatsApp message an invited number receives. It names
// who invites, so the person knows whether to accept, and says how.
func InviteText(inviter licensingdomain.Phone, household string, role Role, expires time.Time) string {
	who := "Un membre de Kergui"
	if inviter != "" {
		who = "Le " + string(inviter)
	}
	what := "lecteur (voir les appareils)"
	if role == RoleAdmin {
		what = "administrateur (voir et gérer les appareils)"
	}
	return fmt.Sprintf("Kergui : %s vous invite dans son foyer « %s » en tant que %s. "+
		"Pour accepter, installez l'app Kergui et connectez-vous avec ce numéro avant le %s. "+
		"Si vous ne connaissez pas ce numéro, ignorez ce message.",
		who, household, what, expires.Format("02/01/2006"))
}

// CancelInvite withdraws an open invite.
func (m *Module) CancelInvite(ctx context.Context, caller tenantaccess.Caller, inviteID string) error {
	if !caller.IsOwner() {
		return ErrOwnerOnly
	}
	if _, err := uuid.Parse(inviteID); err != nil {
		return ErrNoSuchInvite
	}
	tag, err := m.cfg.Pool.Exec(ctx, `UPDATE hub_identity_invites SET revoked_at = $3
		WHERE id = $1 AND tenant_id = $2 AND accepted_at IS NULL AND revoked_at IS NULL`,
		inviteID, caller.TenantID, m.now().UTC())
	if err != nil {
		return fmt.Errorf("identity: cancel invite: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSuchInvite
	}
	m.auditEvent(ctx, caller, auditdomain.ACTION_MEMBER_REVOKED, "invite", inviteID, nil)
	return nil
}

// ChangeRole makes a member an admin or a viewer. The kernel has no use case
// for it, so the role is updated in place: a new membership row would leave
// a revoked one beside it, and the kernel's FindByUserAndTenant reads
// either.
func (m *Module) ChangeRole(ctx context.Context, caller tenantaccess.Caller, membershipID string, role Role) (Member, error) {
	if !caller.IsOwner() {
		return Member{}, ErrOwnerOnly
	}
	kernelRole, ok := role.kernelRole()
	if !ok {
		return Member{}, ErrInvalidRole
	}
	ms, err := m.memberOf(ctx, caller.TenantID, membershipID)
	if err != nil {
		return Member{}, err
	}
	if ms.IsOwner() {
		return Member{}, ErrOwnerUntouched
	}
	if _, err := m.cfg.Pool.Exec(ctx, `UPDATE memberships SET role = $2, updated_at = now() WHERE id = $1 AND status = 'ACTIVE'`,
		ms.ID, string(kernelRole)); err != nil {
		return Member{}, fmt.Errorf("identity: change role: %w", err)
	}
	m.auditEvent(ctx, caller, auditdomain.AuditAction("MEMBER_ROLE_CHANGED"), "membership", ms.ID,
		map[string]any{"user_id": ms.UserID, "from": string(ms.Role), "to": string(kernelRole)})
	phone, err := m.phoneOf(ctx, ms.UserID)
	if err != nil {
		return Member{}, err
	}
	return Member{MembershipID: ms.ID, UserID: ms.UserID, Role: role, Phone: phone, JoinedAt: ms.JoinedAt}, nil
}

// RemoveMember revokes a membership with the kernel's use case, then ends
// the member's sessions on the household: their refresh tokens scoped to it
// are revoked (their access token already stops at the membership check),
// and so are the WhatsApp alerts they receive for it.
func (m *Module) RemoveMember(ctx context.Context, caller tenantaccess.Caller, membershipID string) error {
	if !caller.IsOwner() {
		return ErrOwnerOnly
	}
	if m.cfg.Revoker == nil {
		return ErrMembersDisabled
	}
	ms, err := m.memberOf(ctx, caller.TenantID, membershipID)
	if err != nil {
		return err
	}
	if ms.IsOwner() {
		return ErrOwnerUntouched
	}
	return txctx.ExecInTx(ctx, m.cfg.Pool, func(ctx context.Context) error {
		if _, err := m.cfg.Revoker.Execute(ctx, ms.ID, caller.UserID); err != nil {
			return err
		}
		// The kernel revokes one refresh token or all of a user's; a member
		// keeps their own household, so only this one's are revoked.
		if _, err := txctx.DB(ctx, m.cfg.Pool).Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now()
			WHERE user_id = $1 AND tenant_id = $2 AND revoked_at IS NULL`, ms.UserID, caller.TenantID); err != nil {
			return fmt.Errorf("identity: revoke the member's sessions: %w", err)
		}
		if m.cfg.OnRemoved != nil {
			if err := m.cfg.OnRemoved(ctx, caller.TenantID, ms.UserID); err != nil {
				return fmt.Errorf("identity: after removal: %w", err)
			}
		}
		return nil
	})
}

// memberOf finds an active membership of the caller's household.
func (m *Module) memberOf(ctx context.Context, tenantID, membershipID string) (*memberdomain.Membership, error) {
	if _, err := uuid.Parse(membershipID); err != nil {
		return nil, ErrNoSuchMember
	}
	ms, err := m.cfg.Members.FindByID(ctx, membershipID)
	if errors.Is(err, memberdomain.ErrMembershipNotFound) {
		return nil, ErrNoSuchMember
	}
	if err != nil {
		return nil, fmt.Errorf("identity: member: %w", err)
	}
	if ms.TenantID != tenantID || !ms.IsActive() {
		return nil, ErrNoSuchMember
	}
	return ms, nil
}

// isMember reports whether the number's user is an active member of the
// household.
func (m *Module) isMember(ctx context.Context, phone licensingdomain.Phone, tenantID string) (bool, error) {
	var userID string
	err := txctx.DB(ctx, m.cfg.Pool).QueryRow(ctx, `SELECT h.user_id FROM hub_identity_households h
		JOIN hub_licensing_accounts a ON a.id = h.account_id WHERE a.phone = $1`, string(phone)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	ms, err := m.cfg.Members.FindByUserAndTenant(ctx, userID, tenantID)
	if errors.Is(err, memberdomain.ErrMembershipNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ms.IsActive(), nil
}

// phonesOf maps users to the numbers they sign in with.
func (m *Module) phonesOf(ctx context.Context, userIDs []string) (map[string]licensingdomain.Phone, error) {
	out := make(map[string]licensingdomain.Phone, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := m.cfg.Pool.Query(ctx, `SELECT h.user_id::text, a.phone FROM hub_identity_households h
		JOIN hub_licensing_accounts a ON a.id = h.account_id WHERE h.user_id::text = ANY($1)`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("identity: numbers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, phone string
		if err := rows.Scan(&id, &phone); err != nil {
			return nil, fmt.Errorf("identity: numbers: %w", err)
		}
		out[id] = licensingdomain.Phone(phone)
	}
	return out, rows.Err()
}

func (m *Module) phoneOf(ctx context.Context, userID string) (licensingdomain.Phone, error) {
	p, err := m.phonesOf(ctx, []string{userID})
	return p[userID], err
}

func (m *Module) openInvites(ctx context.Context, tenantID string) ([]Invite, error) {
	rows, err := m.cfg.Pool.Query(ctx, `SELECT id::text, phone, role, created_at, expires_at FROM hub_identity_invites
		WHERE tenant_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		ORDER BY created_at`, tenantID, m.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("identity: invites: %w", err)
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var (
			inv         Invite
			phone, role string
		)
		if err := rows.Scan(&inv.ID, &phone, &role, &inv.CreatedAt, &inv.ExpiresAt); err != nil {
			return nil, fmt.Errorf("identity: invites: %w", err)
		}
		inv.TenantID, inv.Phone, inv.Role = tenantID, licensingdomain.Phone(phone), roleOf(memberdomain.MemberRole(role))
		out = append(out, inv)
	}
	return out, rows.Err()
}

// acceptInvites turns the open invites of a number into memberships, inside
// SignIn's transaction. A member removed earlier gets their membership row
// back rather than a second one beside the revoked row.
func (m *Module) acceptInvites(ctx context.Context, user string, phone licensingdomain.Phone) error {
	db := txctx.DB(ctx, m.cfg.Pool)
	now := m.now().UTC()
	rows, err := db.Query(ctx, `SELECT id::text, tenant_id::text, role, invited_by::text FROM hub_identity_invites
		WHERE phone = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > $2
		ORDER BY created_at FOR UPDATE`, string(phone), now)
	if err != nil {
		return err
	}
	type open struct{ id, tenant, role, by string }
	var invites []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.tenant, &o.role, &o.by); err != nil {
			rows.Close()
			return err
		}
		invites = append(invites, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, inv := range invites {
		membershipID, err := m.join(ctx, user, inv.tenant, inv.by, memberdomain.MemberRole(inv.role), now)
		if err != nil {
			return err
		}
		if _, err := db.Exec(ctx, `UPDATE hub_identity_invites SET accepted_at = $2, membership_id = $3 WHERE id = $1`,
			inv.id, now, membershipID); err != nil {
			return err
		}
		if m.cfg.AuditLogger != nil {
			m.cfg.AuditLogger.Execute(ctx, audit.LogEventParams{
				TenantID: inv.tenant, ActorID: user, Action: auditdomain.ACTION_MEMBER_ACCEPTED,
				TargetType: "membership", TargetID: membershipID,
				Metadata: map[string]any{"invite": inv.id, "role": inv.role, "flow": "whatsapp_otp"},
			})
		}
	}
	return nil
}

// join makes the user an active member of the household with the role.
func (m *Module) join(ctx context.Context, userID, tenantID, invitedBy string, role memberdomain.MemberRole, now time.Time) (string, error) {
	existing, err := m.cfg.Members.FindByUserAndTenant(ctx, userID, tenantID)
	switch {
	case errors.Is(err, memberdomain.ErrMembershipNotFound):
		ms := &memberdomain.Membership{
			ID: uuid.NewString(), UserID: userID, TenantID: tenantID, Role: role,
			Status: memberdomain.MEMBER_STATUS_ACTIVE, InvitedBy: invitedBy, JoinedAt: &now,
			CreatedAt: now, UpdatedAt: now,
		}
		return ms.ID, m.cfg.Members.Save(ctx, ms)
	case err != nil:
		return "", err
	case existing.IsOwner() || existing.IsActive():
		// Already in (the owner of it, or an earlier invite): nothing to do.
		return existing.ID, nil
	}
	if err := m.cfg.Members.UpdateStatus(ctx, existing.ID, memberdomain.MEMBER_STATUS_ACTIVE, &now); err != nil {
		return "", err
	}
	if _, err := txctx.DB(ctx, m.cfg.Pool).Exec(ctx, `UPDATE memberships SET role = $2 WHERE id = $1`, existing.ID, string(role)); err != nil {
		return "", err
	}
	return existing.ID, nil
}

func (m *Module) auditEvent(ctx context.Context, caller tenantaccess.Caller, action auditdomain.AuditAction, targetType, targetID string, meta map[string]any) {
	if m.cfg.AuditLogger == nil {
		return
	}
	m.cfg.AuditLogger.Execute(ctx, audit.LogEventParams{
		TenantID: caller.TenantID, ActorID: caller.UserID, Action: action,
		TargetType: targetType, TargetID: targetID, Metadata: meta,
	})
}

// joinedHousehold is the household other than its own that the user joined
// most recently, or "" when it is a member of none. A member signs in to the
// household they were invited into; their own stays theirs (RFC-005, #42).
func (m *Module) joinedHousehold(ctx context.Context, userID, own string) (string, error) {
	all, err := m.cfg.Members.ListByUser(ctx, userID)
	if err != nil {
		return "", err
	}
	var (
		best string
		at   time.Time
	)
	for _, ms := range all {
		if !ms.IsActive() || ms.TenantID == own || ms.IsOwner() {
			continue
		}
		if j := ms.JoinedAt; j != nil && (best == "" || j.After(at)) {
			best, at = ms.TenantID, *j
		} else if best == "" {
			best = ms.TenantID
		}
	}
	return best, nil
}
