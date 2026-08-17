package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/knyazushka/vcard/internal/domain"
)

// InvitationRepo — хранилище приглашений.
//
// Многотабличные сценарии (создание приглашения вместе с письмом, приём
// вместе с регистрацией) держат транзакцию внутри себя, а не через общий
// менеджер транзакций: границы у них жёсткие и известны заранее, а лишний
// слой абстракции скрыл бы, что именно обязано быть атомарным.
type InvitationRepo struct {
	db *pgxpool.Pool
}

// NewInvitationRepo создаёт хранилище приглашений.
func NewInvitationRepo(db *pgxpool.Pool) *InvitationRepo { return &InvitationRepo{db: db} }

const invitationColumns = `
	i.id, i.company_id, c.name, coalesce(c.logo_key, ''), i.email, i.role, i.status,
	i.invited_by, inviter.email, i.expires_at, i.accepted_at, i.last_sent_at, i.created_at`

func scanInvitation(row pgx.CollectableRow) (domain.Invitation, error) {
	var inv domain.Invitation
	return inv, row.Scan(&inv.ID, &inv.CompanyID, &inv.CompanyName, &inv.CompanyLogoKey,
		&inv.Email, &inv.Role, &inv.Status, &inv.InvitedBy, &inv.InvitedByEmail,
		&inv.ExpiresAt, &inv.AcceptedAt, &inv.LastSentAt, &inv.CreatedAt)
}

// OutboxMessage — письмо, поставленное в очередь.
type OutboxMessage struct {
	Kind    string
	To      string
	Payload map[string]any
}

// Create заводит приглашение и кладёт письмо в outbox одной транзакцией.
//
// Либо есть и приглашение, и письмо, либо нет ничего. Синхронная отправка
// прямо здесь означала бы, что недоступность почтового провайдера роняет
// создание приглашения, а падение между коммитом и отправкой оставляет
// приглашение без письма — и администратор об этом не узнает.
func (r *InvitationRepo) Create(
	ctx context.Context, inv domain.Invitation, tokenHash []byte, msg OutboxMessage,
) (domain.Invitation, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.Invitation{}, fmt.Errorf("begin create invitation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		insert into invitations (id, company_id, email, role, token_hash, invited_by, expires_at)
		values ($1, $2, $3, $4, $5, $6, $7)`

	inv.ID = uuid.New()
	if _, err := tx.Exec(ctx, q, inv.ID, inv.CompanyID, inv.Email, inv.Role,
		tokenHash, inv.InvitedBy, inv.ExpiresAt); err != nil {
		return domain.Invitation{}, mapError(err)
	}

	if err := enqueueEmail(ctx, tx, msg); err != nil {
		return domain.Invitation{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Invitation{}, fmt.Errorf("commit create invitation: %w", mapError(err))
	}

	return r.ByID(ctx, inv.CompanyID, inv.ID)
}

// ByID отдаёт приглашение компании.
func (r *InvitationRepo) ByID(ctx context.Context, companyID, id uuid.UUID) (domain.Invitation, error) {
	q := `
		select ` + invitationColumns + `
		  from invitations i
		  join companies c on c.id = i.company_id
		  join users inviter on inviter.id = i.invited_by
		 where i.company_id = $1 and i.id = $2`

	rows, err := r.db.Query(ctx, q, companyID, id)
	if err != nil {
		return domain.Invitation{}, mapError(err)
	}
	defer rows.Close()

	inv, err := pgx.CollectExactlyOneRow(rows, scanInvitation)
	if err != nil {
		return domain.Invitation{}, mapError(err)
	}
	return inv, nil
}

// ByTokenHash находит приглашение по хэшу токена из письма.
func (r *InvitationRepo) ByTokenHash(ctx context.Context, hash []byte) (domain.Invitation, error) {
	q := `
		select ` + invitationColumns + `
		  from invitations i
		  join companies c on c.id = i.company_id
		  join users inviter on inviter.id = i.invited_by
		 where i.token_hash = $1`

	rows, err := r.db.Query(ctx, q, hash)
	if err != nil {
		return domain.Invitation{}, mapError(err)
	}
	defer rows.Close()

	inv, err := pgx.CollectExactlyOneRow(rows, scanInvitation)
	if err != nil {
		return domain.Invitation{}, mapError(err)
	}
	return inv, nil
}

// List отдаёт приглашения компании, при желании отфильтрованные по состоянию.
func (r *InvitationRepo) List(
	ctx context.Context, companyID uuid.UUID, status string, page domain.Page,
) ([]domain.Invitation, int64, error) {
	const countQ = `
		select count(*) from invitations
		 where company_id = $1 and ($2 = '' or status = $2)`

	var total int64
	if err := r.db.QueryRow(ctx, countQ, companyID, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count invitations: %w", mapError(err))
	}

	q := `
		select ` + invitationColumns + `
		  from invitations i
		  join companies c on c.id = i.company_id
		  join users inviter on inviter.id = i.invited_by
		 where i.company_id = $1 and ($2 = '' or i.status = $2)
		 order by i.created_at desc
		 limit $3 offset $4`

	rows, err := r.db.Query(ctx, q, companyID, status, page.Limit, page.Offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, scanInvitation)
	if err != nil {
		return nil, 0, fmt.Errorf("scan invitations: %w", err)
	}
	return out, total, nil
}

// Revoke переводит приглашение в REVOKED.
//
// Строка не удаляется: история «кого звали и кто отозвал» переживает отзыв,
// а ссылка из письма перестаёт работать немедленно.
func (r *InvitationRepo) Revoke(ctx context.Context, companyID, id uuid.UUID) error {
	const q = `
		update invitations set status = 'REVOKED'
		 where company_id = $1 and id = $2 and status = 'PENDING'`

	tag, err := r.db.Exec(ctx, q, companyID, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Resend выпускает новый токен и ставит письмо в очередь заново.
//
// Прежний токен инвалидируется: в базе его нет, только хэш, поэтому
// повторить исходное письмо технически невозможно. Срок отсчитывается заново.
func (r *InvitationRepo) Resend(
	ctx context.Context, companyID, id uuid.UUID, tokenHash []byte, expiresAt time.Time, msg OutboxMessage,
) (domain.Invitation, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return domain.Invitation{}, fmt.Errorf("begin resend: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		update invitations
		   set token_hash = $3, expires_at = $4, last_sent_at = now()
		 where company_id = $1 and id = $2 and status = 'PENDING'`

	tag, err := tx.Exec(ctx, q, companyID, id, tokenHash, expiresAt)
	if err != nil {
		return domain.Invitation{}, mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Invitation{}, domain.ErrNotFound
	}

	if err := enqueueEmail(ctx, tx, msg); err != nil {
		return domain.Invitation{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Invitation{}, fmt.Errorf("commit resend: %w", mapError(err))
	}
	return r.ByID(ctx, companyID, id)
}

// AcceptResult — что получилось в итоге приёма приглашения.
type AcceptResult struct {
	UserID      uuid.UUID
	CompanyID   uuid.UUID
	Role        domain.Role
	ProfileID   uuid.UUID
	ProfileSlug string
	Registered  bool
}

// Accept принимает приглашение одной транзакцией.
//
// Если учётки ещё нет, в этой же транзакции создаются пользователь, членство
// и черновик профиля. Разорвать её нельзя: половина результата — это
// пользователь без компании либо сотрудник без страницы, и оба состояния
// не имеют осмысленной обработки в остальном приложении.
//
// Почта считается подтверждённой сразу: письмо со ссылкой уже дошло,
// второе подтверждение было бы ритуалом.
func (r *InvitationRepo) Accept(
	ctx context.Context, inv domain.Invitation, userID uuid.UUID,
	newUser *domain.User, slugCandidates []string, now time.Time,
) (AcceptResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return AcceptResult{}, fmt.Errorf("begin accept: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if newUser != nil {
		const insertUser = `
			insert into users (id, email, email_verified, password_hash)
			values ($1, $2, true, $3)`

		if _, err := tx.Exec(ctx, insertUser, newUser.ID, newUser.Email, newUser.PasswordHash); err != nil {
			if isConstraint(err, "users_email_key") {
				return AcceptResult{}, domain.ErrEmailTaken
			}
			return AcceptResult{}, fmt.Errorf("create user: %w", mapError(err))
		}
		userID = newUser.ID
	}

	const insertMembership = `
		insert into memberships (user_id, company_id, role)
		values ($1, $2, $3)
		on conflict (user_id, company_id) do nothing`

	if _, err := tx.Exec(ctx, insertMembership, userID, inv.CompanyID, inv.Role); err != nil {
		return AcceptResult{}, fmt.Errorf("create membership: %w", mapError(err))
	}

	profileID, slug, err := ensureProfile(ctx, tx, userID, inv.CompanyID, slugCandidates)
	if err != nil {
		return AcceptResult{}, err
	}

	const closeInvitation = `
		update invitations
		   set status = 'ACCEPTED', accepted_at = $3, accepted_by = $4
		 where id = $1 and company_id = $2 and status = 'PENDING'`

	if _, err := tx.Exec(ctx, closeInvitation, inv.ID, inv.CompanyID, now, userID); err != nil {
		return AcceptResult{}, fmt.Errorf("close invitation: %w", mapError(err))
	}

	if err := tx.Commit(ctx); err != nil {
		return AcceptResult{}, fmt.Errorf("commit accept: %w", mapError(err))
	}

	return AcceptResult{
		UserID:      userID,
		CompanyID:   inv.CompanyID,
		Role:        inv.Role,
		ProfileID:   profileID,
		ProfileSlug: slug,
		Registered:  newUser != nil,
	}, nil
}

// ensureProfile заводит черновик визитки, если его ещё нет.
//
// Адрес подбирается перебором заготовок со вставкой: предварительный SELECT
// не спасает — между проверкой и записью успевает вклиниться другая
// регистрация, и «свободный» адрес оказывается занят.
func ensureProfile(
	ctx context.Context, tx pgx.Tx, userID, companyID uuid.UUID, candidates []string,
) (uuid.UUID, string, error) {
	const existing = `
		select id, slug::text from profiles where user_id = $1 and company_id = $2`

	var id uuid.UUID
	var slug string
	err := tx.QueryRow(ctx, existing, userID, companyID).Scan(&id, &slug)
	if err == nil {
		return id, slug, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", fmt.Errorf("lookup profile: %w", mapError(err))
	}

	const insert = `
		insert into profiles (id, user_id, company_id, slug)
		values ($1, $2, $3, $4)`

	for _, candidate := range candidates {
		id = uuid.New()

		// Вложенная точка сохранения: конфликт по слагу иначе делает всю
		// транзакцию непригодной, и следующая попытка обречена.
		if _, err := tx.Exec(ctx, "savepoint slug_attempt"); err != nil {
			return uuid.Nil, "", fmt.Errorf("savepoint: %w", err)
		}

		_, err := tx.Exec(ctx, insert, id, userID, companyID, candidate)
		if err == nil {
			return id, candidate, nil
		}

		mapped := mapError(err)
		if !errors.Is(mapped, domain.ErrSlugTaken) && !errors.Is(mapped, domain.ErrSlugReserved) {
			return uuid.Nil, "", fmt.Errorf("create profile: %w", mapped)
		}

		if _, err := tx.Exec(ctx, "rollback to savepoint slug_attempt"); err != nil {
			return uuid.Nil, "", fmt.Errorf("rollback savepoint: %w", err)
		}
	}

	return uuid.Nil, "", domain.ErrSlugTaken
}

// enqueueEmail кладёт письмо в outbox.
func enqueueEmail(ctx context.Context, q execer, msg OutboxMessage) error {
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		return fmt.Errorf("marshal email payload: %w", err)
	}

	const insert = `
		insert into email_outbox (kind, to_email, payload) values ($1, $2, $3)`

	if _, err := q.Exec(ctx, insert, msg.Kind, msg.To, payload); err != nil {
		return fmt.Errorf("enqueue email: %w", mapError(err))
	}
	return nil
}
