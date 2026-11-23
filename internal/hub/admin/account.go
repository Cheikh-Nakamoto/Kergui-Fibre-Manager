package admin

import (
	"context"
	"errors"
	"fmt"

	authdomain "github.com/agile-factory/kernel/platform/auth/domain"
	authinfra "github.com/agile-factory/kernel/platform/auth/infrastructure"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MinPasswordLen is the shortest back-office password accepted.
const MinPasswordLen = 12

// CreateAdmin makes email a platform administrator, the only kind of
// account the back-office lets in. An existing account is promoted (and its
// password replaced when one is given); a new one is created, verified and
// active. It is run by hand on the hub's host: `kergui-hub admin create`.
func CreateAdmin(ctx context.Context, pool *pgxpool.Pool, email, password string) (created bool, err error) {
	addr, err := authdomain.NewEmail(email)
	if err != nil {
		return false, fmt.Errorf("admin: %w", err)
	}
	if password != "" && len(password) < MinPasswordLen {
		return false, fmt.Errorf("admin: the password must be at least %d characters", MinPasswordLen)
	}
	var hashed string
	if password != "" {
		if hashed, err = authinfra.NewBcryptHasher(0).Hash(password); err != nil {
			return false, fmt.Errorf("admin: hash: %w", err)
		}
	}

	var id string
	err = pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, addr.String()).Scan(&id)
	switch {
	case err == nil:
		if hashed != "" {
			_, err = pool.Exec(ctx, `UPDATE users SET role = 'ROLE_ADMIN', status = 'ACTIVE', email_verified = true,
				password = $2, updated_at = now() WHERE id = $1`, id, hashed)
		} else {
			_, err = pool.Exec(ctx, `UPDATE users SET role = 'ROLE_ADMIN', updated_at = now() WHERE id = $1`, id)
		}
		if err != nil {
			return false, fmt.Errorf("admin: promote: %w", err)
		}
		return false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("admin: look up %s: %w", addr, err)
	}
	if hashed == "" {
		return false, errors.New("admin: a new account needs a password (KERGUI_ADMIN_PASSWORD)")
	}
	user := authdomain.NewVerifiedUser(uuid.NewString(), addr, hashed, authdomain.ROLE_ADMIN)
	if err := authinfra.NewPostgresUserRepository(pool).Save(ctx, user); err != nil {
		return false, fmt.Errorf("admin: create: %w", err)
	}
	return true, nil
}
