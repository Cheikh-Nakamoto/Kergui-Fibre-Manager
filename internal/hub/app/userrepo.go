package app

import (
	"context"
	"errors"

	"github.com/agile-factory/kernel/kernel/txctx"
	authdomain "github.com/agile-factory/kernel/platform/auth/domain"
	authinfra "github.com/agile-factory/kernel/platform/auth/infrastructure"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// userRepo works around a kernel bug (agile-factory/kernel a4c4515):
// PostgresUserRepository scans the email column straight into domain.Email,
// a struct without sql.Scanner, so pgx fails every FindByEmail / FindByID and
// login always answers "invalid credentials". The reads are redone here with
// the email scanned into a string; Save and UpdateEmailVerified are the
// kernel's. Drop this type once agile-factory/kernel#63 is merged and pinned.
type userRepo struct {
	*authinfra.PostgresUserRepository
	pool *pgxpool.Pool
}

func newUserRepo(pool *pgxpool.Pool) userRepo {
	return userRepo{PostgresUserRepository: authinfra.NewPostgresUserRepository(pool), pool: pool}
}

const selectUser = `SELECT id, email, password, role, email_verified, status, created_at, updated_at FROM users `

func (r userRepo) FindByEmail(ctx context.Context, email authdomain.Email) (*authdomain.User, error) {
	return r.findOne(ctx, selectUser+`WHERE email = $1`, email.String())
}

func (r userRepo) FindByID(ctx context.Context, id string) (*authdomain.User, error) {
	return r.findOne(ctx, selectUser+`WHERE id = $1`, id)
}

func (r userRepo) findOne(ctx context.Context, query, arg string) (*authdomain.User, error) {
	var (
		u             authdomain.User
		email, status string
	)
	err := txctx.DB(ctx, r.pool).QueryRow(ctx, query, arg).Scan(
		&u.ID, &email, &u.HashedPassword, &u.Role, &u.EmailVerified, &status, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, authdomain.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Email = authdomain.Email{Value: email}
	u.Status = authdomain.UserStatus(status)
	return &u, nil
}
