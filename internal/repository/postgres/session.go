package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/knyazushka/vcard/internal/domain"
)

// SessionRepo — хранилище refresh-сессий.
type SessionRepo struct {
	db *pgxpool.Pool
}

// NewSessionRepo создаёт хранилище сессий.
func NewSessionRepo(db *pgxpool.Pool) *SessionRepo { return &SessionRepo{db: db} }

// Create открывает новую цепочку сессий.
func (r *SessionRepo) Create(ctx context.Context, s domain.Session, tokenHash []byte) error {
	if err := insertSession(ctx, r.db, s, tokenHash); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// ByTokenHash ищет сессию по хэшу предъявленного токена.
func (r *SessionRepo) ByTokenHash(ctx context.Context, hash []byte) (domain.Session, error) {
	const q = `
		select id, family_id, user_id, issued_at, expires_at, used_at, revoked_at,
		       coalesce(user_agent, ''), coalesce(host(ip), '')
		  from sessions
		 where token_hash = $1`

	var s domain.Session
	err := r.db.QueryRow(ctx, q, hash).Scan(
		&s.ID, &s.FamilyID, &s.UserID, &s.IssuedAt, &s.ExpiresAt,
		&s.UsedAt, &s.RevokedAt, &s.UserAgent, &s.IP,
	)
	if err != nil {
		mapped := mapError(err)
		return domain.Session{}, mapped
	}
	return s, nil
}

// MarkUsed помечает сессию обменянной.
func (r *SessionRepo) MarkUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	const q = `update sessions set used_at = $2 where id = $1 and used_at is null`

	if _, err := r.db.Exec(ctx, q, id, at); err != nil {
		mapped := mapError(err)
		return fmt.Errorf("mark session used: %w", mapped)
	}
	return nil
}

// RevokeFamily гасит всю цепочку ротаций одного входа.
func (r *SessionRepo) RevokeFamily(ctx context.Context, familyID uuid.UUID, at time.Time) error {
	const q = `update sessions set revoked_at = $2 where family_id = $1 and revoked_at is null`

	if _, err := r.db.Exec(ctx, q, familyID, at); err != nil {
		mapped := mapError(err)
		return fmt.Errorf("revoke family: %w", mapped)
	}
	return nil
}

// RevokeAllForUser гасит все сессии пользователя.
func (r *SessionRepo) RevokeAllForUser(ctx context.Context, userID uuid.UUID, at time.Time) error {
	const q = `update sessions set revoked_at = $2 where user_id = $1 and revoked_at is null`

	if _, err := r.db.Exec(ctx, q, userID, at); err != nil {
		mapped := mapError(err)
		return fmt.Errorf("revoke user sessions: %w", mapped)
	}
	return nil
}

// RotateWithinFamily помечает старую сессию использованной и заводит новую
// в той же цепочке — одной транзакцией.
//
// Атомарность здесь не украшение: если пометка пройдёт, а вставка нет,
// пользователь останется без действующего refresh и будет разлогинен;
// если наоборот — старый токен переживёт ротацию, и обнаружение повторного
// использования перестанет работать.
//
// UPDATE с условием used_at is null защищает от гонки двух одновременных
// обменов одним токеном: второй не найдёт строку и увидит ошибку.
func (r *SessionRepo) RotateWithinFamily(
	ctx context.Context, oldID uuid.UUID, next domain.Session, nextHash []byte, at time.Time,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rotate: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const markUsed = `
		update sessions set used_at = $2
		 where id = $1 and used_at is null and revoked_at is null`

	tag, err := tx.Exec(ctx, markUsed, oldID, at)
	if err != nil {
		mapped := mapError(err)
		return fmt.Errorf("mark session used: %w", mapped)
	}
	if tag.RowsAffected() == 0 {
		// Кто-то обменял этот токен между чтением и записью.
		return domain.ErrSessionInvalid
	}

	if err := insertSession(ctx, tx, next, nextHash); err != nil {
		return fmt.Errorf("insert rotated session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rotate: %w", err)
	}
	return nil
}

// DeleteExpired убирает протухшие и отозванные сессии. Вызывается фоновой
// задачей: таблица растёт с каждым входом и без чистки живёт вечно.
func (r *SessionRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	const q = `
		delete from sessions
		 where expires_at < $1
		    or (revoked_at is not null and revoked_at < $1)`

	tag, err := r.db.Exec(ctx, q, before)
	if err != nil {
		mapped := mapError(err)
		return 0, fmt.Errorf("delete expired sessions: %w", mapped)
	}
	return tag.RowsAffected(), nil
}

// execer покрывает и пул, и транзакцию — вставка одна на оба случая.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insertSession(ctx context.Context, q execer, s domain.Session, tokenHash []byte) error {
	const stmt = `
		insert into sessions (id, family_id, user_id, token_hash, issued_at, expires_at, user_agent, ip)
		values ($1, $2, $3, $4, $5, $6, nullif($7, ''), nullif($8, '')::inet)`

	_, err := q.Exec(ctx, stmt,
		s.ID, s.FamilyID, s.UserID, tokenHash, s.IssuedAt, s.ExpiresAt, s.UserAgent, s.IP)
	if err != nil {
		mapped := mapError(err)
		return mapped
	}
	return nil
}
