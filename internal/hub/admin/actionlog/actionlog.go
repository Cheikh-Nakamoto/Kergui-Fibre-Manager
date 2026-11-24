// Package actionlog is the back-office's action log (issue #77, parent #67):
// every sensitive gesture a platform administrator makes, with who, when,
// what and why.
//
// It is not a table of its own: a gesture is a row of the kernel's
// audit_logs (actor_id = the admin, action = the kind, target_type and
// target_id, occurred_at), written through the kernel's audit outbox like
// every other audit row. What audit_logs lacks, the reason and a readable
// summary, rides in its metadata under the keys "reason" and "summary", next
// to the gesture's details. The admin module reads the log back, filtered to
// the kinds listed here.
//
// Every admin write goes through Record, and every new kind is added to
// Kinds and to the audit_action enum (a migration of the admin module).
package actionlog

import (
	"context"

	kernelauth "github.com/agile-factory/kernel/kernel/auth"
	"github.com/agile-factory/kernel/platform/audit"
	auditdomain "github.com/agile-factory/kernel/platform/audit/domain"
)

// The kinds of admin gestures.
const (
	// KindPassGranted is a pass granted by hand (support), with a reason.
	KindPassGranted auditdomain.AuditAction = "PASS_GRANTED_BY_SUPPORT"
	// KindReleasePublished and the next three are the « Versions » page's
	// writes (issue #71).
	KindReleasePublished   auditdomain.AuditAction = "RELEASE_PUBLISHED"
	KindReleaseWithdrawn   auditdomain.AuditAction = "RELEASE_WITHDRAWN"
	KindReleaseNotesEdited auditdomain.AuditAction = "RELEASE_NOTES_EDITED"
	KindReleaseMinimumSet  auditdomain.AuditAction = "RELEASE_MINIMUM_SET"
	// KindWhatsAppLinkRequested is a pairing code asked for the hub's own
	// WhatsApp session; KindWhatsAppUnlinked is that session logged out.
	KindWhatsAppLinkRequested auditdomain.AuditAction = "WHATSAPP_SESSION_LINK_REQUESTED"
	KindWhatsAppUnlinked      auditdomain.AuditAction = "WHATSAPP_SESSION_UNLINKED"
	// KindPassExtended, KindPassTransferred and KindSessionsRevoked are the
	// « Support » page's writes (issue #78): a pass extended by some days, a
	// pass moved to another box past the customer's limit, and every phone
	// of an account signed out.
	KindPassExtended    auditdomain.AuditAction = "PASS_EXTENDED"
	KindPassTransferred auditdomain.AuditAction = "PASS_TRANSFERRED_BY_ADMIN"
	KindSessionsRevoked auditdomain.AuditAction = "SESSIONS_REVOKED"
	// KindPaymentRechecked is a payment re-checked at NabooPay from the
	// « Paiements » page (issue #79).
	KindPaymentRechecked auditdomain.AuditAction = "PAYMENT_RECHECKED"
)

// Kinds is every admin gesture kind: the log's read side shows these and
// nothing else of audit_logs.
var Kinds = []auditdomain.AuditAction{
	KindPassGranted,
	KindReleasePublished, KindReleaseWithdrawn, KindReleaseNotesEdited, KindReleaseMinimumSet,
	KindWhatsAppLinkRequested, KindWhatsAppUnlinked,
	KindPassExtended, KindPassTransferred, KindSessionsRevoked,
	KindPaymentRechecked,
}

// IsKind reports whether s is an admin gesture kind.
func IsKind(s string) bool {
	for _, k := range Kinds {
		if string(k) == s {
			return true
		}
	}
	return false
}

// Target types.
const (
	TargetAccount   = "account"
	TargetHousehold = "household"
	TargetBox       = "box"
	TargetPayment   = "payment"
	TargetRelease   = "release"
	TargetMessage   = "message"
	TargetWhatsApp  = "whatsapp"
)

// Metadata keys the read side lifts out of the details.
const (
	MetaReason  = "reason"
	MetaSummary = "summary"
)

// Gesture is one admin action.
type Gesture struct {
	Kind auditdomain.AuditAction
	// TenantID is the household concerned, when there is one.
	TenantID string
	// TargetType is one of the Target* constants; TargetID a UUID, or empty
	// (audit_logs.target_id is a UUID column).
	TargetType string
	TargetID   string
	// Reason is why, as the admin typed it; required where the gesture asks
	// for one (a pass grant).
	Reason string
	// Summary is what happened, in a line a person reads.
	Summary string
	// Details are the gesture's own fields.
	Details map[string]any
}

// Record writes g to the log as the signed-in admin of ctx. Like every audit
// write it never fails the gesture: the outbox carries it, a moment later.
func Record(ctx context.Context, log audit.EventLogger, g Gesture) {
	if log == nil {
		return
	}
	meta := make(map[string]any, len(g.Details)+2)
	for k, v := range g.Details {
		meta[k] = v
	}
	if g.Reason != "" {
		meta[MetaReason] = g.Reason
	}
	if g.Summary != "" {
		meta[MetaSummary] = g.Summary
	}
	log.Execute(ctx, audit.LogEventParams{
		TenantID: g.TenantID, ActorID: kernelauth.UserIDFromContext(ctx), Action: g.Kind,
		TargetType: g.TargetType, TargetID: g.TargetID, Metadata: meta,
	})
}
