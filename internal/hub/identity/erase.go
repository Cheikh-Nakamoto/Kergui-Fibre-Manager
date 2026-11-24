package identity

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/agile-factory/kernel/kernel/txctx"
	"github.com/agile-factory/kernel/platform/audit"
	"github.com/jackc/pgx/v5"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/admin/actionlog"
	licensingdomain "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/hub/licensing/usecase"
)

// Account deletion (#37): the app's « Supprimer mon compte », which Google
// Play requires, erases the account and everything the hub keeps for it,
// in one Postgres transaction. The owner's decision: erase it all, the
// rights, trial and payments included.
//
// A household is erased only when nobody else is in it. The account's own
// household and every household it is a member of are looked at:
//
//   - nobody else in it (no other membership that is not revoked): the
//     household goes whole: every hub_ table's rows of that tenant (owner
//     phones #87, backups and their versions #88, relays, inventory,
//     alerts, trust bundles…), then the kernel tenant, whose memberships,
//     settings, invites and refresh tokens cascade;
//   - someone else in it: the household stays for them, backups included.
//     Only this account leaves it (its membership goes with its kernel
//     user) and, when it was the owner, the owner's phones it declared
//     (#87). The members keep a household without an owner: handing it over
//     is a support case (household transfer).
//
// Then the account's own rows anywhere: its kernel user and what hangs on
// it (refresh tokens, memberships, push tokens, WhatsApp opt-ins, alert and
// message deliveries, member keys), its complaints and their messages (by
// account or by number: WhatsApp complaints carry the number only), the
// open invites and queued WhatsApp messages to its number, its sign-in
// codes, and last the licensing account, whose sessions, subscriptions,
// trial, transfers, payments, Play bindings and reminders cascade.
//
// The action log gets one ACCOUNT_DELETED row (no actor: the account is
// gone), with the masked number and how many households were erased or
// left, written through the kernel's outbox in the same transaction.

var _ usecase.AccountEraser = (*Module)(nil)

// EraseAccount implements usecase.AccountEraser. An account already gone is
// usecase.ErrNotFound.
func (m *Module) EraseAccount(ctx context.Context, a usecase.Account) error {
	if a.ID == "" {
		return errors.New("identity: an account id is required")
	}
	err := txctx.ExecInTx(ctx, m.cfg.Pool, func(ctx context.Context) error {
		db := txctx.DB(ctx, m.cfg.Pool)
		// The same lock as SignIn: no household is created for the account
		// while it is being erased.
		if _, err := db.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('kergui.identity:' || $1, 0))`, string(a.ID)); err != nil {
			return err
		}
		var phone string
		err := db.QueryRow(ctx, `SELECT phone FROM hub_licensing_accounts WHERE id = $1 FOR UPDATE`, string(a.ID)).Scan(&phone)
		if errors.Is(err, pgx.ErrNoRows) {
			return usecase.ErrNotFound
		}
		if err != nil {
			return err
		}

		var userID, home string
		err = db.QueryRow(ctx, `SELECT user_id::text, tenant_id::text FROM hub_identity_households WHERE account_id = $1`,
			string(a.ID)).Scan(&userID, &home)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		erased, left := 0, 0
		if userID != "" {
			if erased, left, err = m.eraseUser(ctx, db, userID, home); err != nil {
				return err
			}
		}
		if err := erasePhone(ctx, db, string(a.ID), phone); err != nil {
			return err
		}
		if m.cfg.BeforeErase != nil {
			if err := m.cfg.BeforeErase(ctx, string(a.ID)); err != nil {
				return err
			}
		}
		if _, err := db.Exec(ctx, `DELETE FROM hub_licensing_accounts WHERE id = $1`, string(a.ID)); err != nil {
			return fmt.Errorf("the licensing account: %w", err)
		}
		if m.cfg.AuditLogger != nil {
			m.cfg.AuditLogger.Execute(ctx, audit.LogEventParams{
				Action: actionlog.KindAccountDeleted, TargetType: "account", TargetID: string(a.ID),
				Metadata: map[string]any{
					"summary":           "Compte supprimé par son titulaire depuis l'application",
					"phone":             licensingdomain.Phone(phone).Masked(),
					"households_erased": erased,
					"households_left":   left,
				},
			})
		}
		return nil
	})
	if err != nil && !errors.Is(err, usecase.ErrNotFound) {
		return fmt.Errorf("identity: erase the account: %w", err)
	}
	return err
}

// eraseUser erases or leaves the user's households, then the kernel user.
func (m *Module) eraseUser(ctx context.Context, db txctx.DBTX, userID, home string) (erased, left int, err error) {
	rows, err := db.Query(ctx, `SELECT tenant_id::text, role = 'ROLE_OWNER' FROM memberships
		WHERE user_id = $1 AND status <> 'REVOKED'`, userID)
	if err != nil {
		return 0, 0, err
	}
	type household struct {
		id    string
		owner bool
	}
	households, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (household, error) {
		var h household
		return h, r.Scan(&h.id, &h.owner)
	})
	if err != nil {
		return 0, 0, err
	}
	if home != "" && !slices.ContainsFunc(households, func(h household) bool { return h.id == home }) {
		households = append(households, household{id: home, owner: true})
	}

	tenantTables, err := hubTables(ctx, db, "tenant_id")
	if err != nil {
		return 0, 0, err
	}
	for _, h := range households {
		var others int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM memberships
			WHERE tenant_id = $1 AND user_id <> $2 AND status <> 'REVOKED'`, h.id, userID).Scan(&others); err != nil {
			return 0, 0, err
		}
		if others > 0 {
			if h.owner {
				if _, err := db.Exec(ctx, `DELETE FROM hub_inventory_owner_devices WHERE tenant_id = $1`, h.id); err != nil {
					return 0, 0, fmt.Errorf("the owner's phones: %w", err)
				}
			}
			left++
			continue
		}
		for _, table := range tenantTables {
			if _, err := db.Exec(ctx, `DELETE FROM `+pgx.Identifier{table}.Sanitize()+` WHERE tenant_id = $1`, h.id); err != nil {
				return 0, 0, fmt.Errorf("household %s, %s: %w", h.id, table, err)
			}
		}
		// The kernel's notification log does not cascade; the tenant's own
		// rows (memberships, settings, invites, refresh tokens) do.
		if _, err := db.Exec(ctx, `DELETE FROM notification_logs WHERE tenant_id = $1`, h.id); err != nil {
			return 0, 0, err
		}
		if _, err := db.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, h.id); err != nil {
			return 0, 0, fmt.Errorf("household %s: %w", h.id, err)
		}
		if err := forgetInOutbox(ctx, db, "tenant_id", h.id); err != nil {
			return 0, 0, err
		}
		erased++
	}

	// The user's rows in the households left to others. A relay the user
	// enrolled stays the household's: it only forgets who enrolled it.
	userTables, err := hubTables(ctx, db, "user_id")
	if err != nil {
		return 0, 0, err
	}
	for _, table := range userTables {
		q := `DELETE FROM ` + pgx.Identifier{table}.Sanitize() + ` WHERE user_id = $1`
		if table == "hub_agents" {
			q = `UPDATE hub_agents SET user_id = NULL WHERE user_id = $1`
		}
		if _, err := db.Exec(ctx, q, userID); err != nil {
			return 0, 0, fmt.Errorf("%s: %w", table, err)
		}
	}
	for _, q := range []string{
		// Neither cascades from users.
		`DELETE FROM invite_tokens WHERE invited_by = $1`,
		`DELETE FROM notification_logs WHERE recipient_id = $1`,
		`DELETE FROM users WHERE id = $1`,
	} {
		if _, err := db.Exec(ctx, q, userID); err != nil {
			return 0, 0, fmt.Errorf("the kernel user: %w", err)
		}
	}
	if err := forgetInOutbox(ctx, db, "actor_id", userID); err != nil {
		return 0, 0, err
	}
	return erased, left, nil
}

// forgetInOutbox blanks an erased tenant or user in the audit events still
// waiting in the kernel's outbox (the sign-in's USER_LOGGED_IN, a member
// invited a moment ago…): audit_logs references tenants and users, so such
// an event would otherwise fail for ever. Blank is NULL to the consumer, as
// for the rows already in audit_logs (ON DELETE SET NULL).
func forgetInOutbox(ctx context.Context, db txctx.DBTX, key, id string) error {
	_, err := db.Exec(ctx, `UPDATE event_outbox SET payload = jsonb_set(payload, ARRAY[$1::text], '""'::jsonb)
		WHERE published_at IS NULL AND event_type = 'audit.log_created' AND payload->>$1 = $2`, key, id)
	if err != nil {
		return fmt.Errorf("the outbox's audit events: %w", err)
	}
	return nil
}

// erasePhone deletes what is kept by the account's id or its number outside
// the cascades: complaints (their messages cascade), open invites, the
// WhatsApp queue and opt-ins, and the sign-in codes.
func erasePhone(ctx context.Context, db txctx.DBTX, accountID, phone string) error {
	if _, err := db.Exec(ctx, `DELETE FROM hub_complaints WHERE account_id = $1 OR phone = $2`, accountID, phone); err != nil {
		return fmt.Errorf("the complaints: %w", err)
	}
	for _, table := range []string{"hub_identity_invites", "hub_whatsapp_queue", "hub_whatsapp_optins", "hub_licensing_otp_challenges"} {
		if _, err := db.Exec(ctx, `DELETE FROM `+pgx.Identifier{table}.Sanitize()+` WHERE phone = $1`, phone); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	return nil
}

// hubTables lists the hub's tables (hub_*) that have the column, children
// first, so a table no module has written yet is never forgotten.
func hubTables(ctx context.Context, db txctx.DBTX, column string) ([]string, error) {
	rows, err := db.Query(ctx, `SELECT c.table_name FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND t.table_type = 'BASE TABLE'
		  AND c.table_name LIKE 'hub\_%' AND c.column_name = $1
		  AND c.table_name <> 'hub_identity_households'
		ORDER BY c.table_name`, column)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
