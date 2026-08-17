package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/knyazushka/vcard/internal/domain"
)

// UserRepo — хранилище пользователей.
type UserRepo struct {
	db *pgxpool.Pool
}

// NewUserRepo создаёт хранилище пользователей.
func NewUserRepo(db *pgxpool.Pool) *UserRepo { return &UserRepo{db: db} }

// Create заводит пользователя; занятый адрес даёт domain.ErrEmailTaken.
func (r *UserRepo) Create(ctx context.Context, u domain.User) error {
	const q = `
		insert into users (id, email, email_verified, password_hash)
		values ($1, $2, $3, $4)`

	_, err := r.db.Exec(ctx, q, u.ID, u.Email, u.EmailVerified, u.PasswordHash)
	if err != nil {
		// Уникальный индекс — единственный надёжный арбитр занятости адреса,
		// поэтому именно его ошибка превращается в доменную, а не результат
		// предварительной проверки.
		if isConstraint(err, "users_email_key") {
			return domain.ErrEmailTaken
		}
		mapped := mapError(err)
		return fmt.Errorf("create user: %w", mapped)
	}
	return nil
}

// ByEmail ищет пользователя по нормализованному адресу.
func (r *UserRepo) ByEmail(ctx context.Context, email string) (domain.User, error) {
	const q = `
		select id, email, email_verified, password_hash, created_at, updated_at
		  from users
		 where email = $1`

	return r.one(ctx, q, email)
}

// ByID ищет пользователя по идентификатору.
func (r *UserRepo) ByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	const q = `
		select id, email, email_verified, password_hash, created_at, updated_at
		  from users
		 where id = $1`

	return r.one(ctx, q, id)
}

func (r *UserRepo) one(ctx context.Context, q string, arg any) (domain.User, error) {
	var u domain.User

	err := r.db.QueryRow(ctx, q, arg).Scan(
		&u.ID, &u.Email, &u.EmailVerified, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		mapped := mapError(err)
		return domain.User{}, mapped
	}
	return u, nil
}

// Memberships отдаёт компании пользователя вместе с ролью и его профилем.
//
// Профиль присоединяется тем же запросом: без этого список компаний
// на главной странице порождал бы по запросу на каждую строку.
func (r *UserRepo) Memberships(ctx context.Context, userID uuid.UUID) ([]domain.Membership, error) {
	const q = `
		select m.company_id,
		       c.name,
		       coalesce(c.logo_key, ''),
		       m.role,
		       coalesce(p.id, '00000000-0000-0000-0000-000000000000'::uuid),
		       m.created_at
		  from memberships m
		  join companies c on c.id = m.company_id
		  left join profiles p
		         on p.user_id = m.user_id
		        and p.company_id = m.company_id
		 where m.user_id = $1
		 order by m.created_at`

	rows, err := r.db.Query(ctx, q, userID)
	if err != nil {
		mapped := mapError(err)
		return nil, fmt.Errorf("query memberships: %w", mapped)
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Membership, error) {
		var m domain.Membership
		err := row.Scan(&m.CompanyID, &m.CompanyName, &m.CompanyLogoKey,
			&m.Role, &m.ProfileID, &m.CreatedAt)
		return m, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan memberships: %w", err)
	}
	return out, nil
}
