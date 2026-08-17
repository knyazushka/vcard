package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxRepo — очередь исходящих писем.
type OutboxRepo struct {
	db *pgxpool.Pool
}

// NewOutboxRepo создаёт хранилище очереди писем.
func NewOutboxRepo(db *pgxpool.Pool) *OutboxRepo { return &OutboxRepo{db: db} }

// OutboxItem — письмо, взятое в работу.
type OutboxItem struct {
	ID       uuid.UUID
	Kind     string
	To       string
	Payload  map[string]any
	Attempts int16
}

// Claim забирает пачку созревших писем и помечает их взятыми в работу.
//
// FOR UPDATE SKIP LOCKED позволяет нескольким рассыльщикам разбирать очередь
// одновременно, не блокируя друг друга и не отправляя одно письмо дважды.
// Ради этого отдельная очередь (Redis, RabbitMQ) не нужна — Postgres умеет
// ровно то, что здесь требуется, и это на один backing service меньше.
//
// next_attempt_at сдвигается вперёд сразу: если процесс упадёт между
// выборкой и отправкой, письмо не зависнет навсегда, а вернётся в очередь.
func (r *OutboxRepo) Claim(ctx context.Context, limit int, leaseFor time.Duration) ([]OutboxItem, error) {
	const q = `
		with due as (
			select id from email_outbox
			 where status = 'PENDING' and next_attempt_at <= now()
			 order by next_attempt_at
			 limit $1
			 for update skip locked
		)
		update email_outbox o
		   set attempts = o.attempts + 1,
		       next_attempt_at = now() + $2::interval
		  from due
		 where o.id = due.id
		returning o.id, o.kind, o.to_email, o.payload, o.attempts`

	rows, err := r.db.Query(ctx, q, limit, leaseFor.String())
	if err != nil {
		return nil, fmt.Errorf("claim outbox: %w", mapError(err))
	}
	defer rows.Close()

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (OutboxItem, error) {
		var it OutboxItem
		var payload []byte
		if err := row.Scan(&it.ID, &it.Kind, &it.To, &payload, &it.Attempts); err != nil {
			return it, err
		}
		if err := json.Unmarshal(payload, &it.Payload); err != nil {
			return it, fmt.Errorf("unmarshal payload: %w", err)
		}
		return it, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan outbox: %w", err)
	}
	return out, nil
}

// MarkSent закрывает письмо как отправленное.
func (r *OutboxRepo) MarkSent(ctx context.Context, id uuid.UUID) error {
	const q = `update email_outbox set status = 'SENT', sent_at = now(), last_error = null where id = $1`

	if _, err := r.db.Exec(ctx, q, id); err != nil {
		return fmt.Errorf("mark sent: %w", mapError(err))
	}
	return nil
}

// MarkFailed откладывает следующую попытку или окончательно сдаётся.
//
// Бесконечно ретраить нельзя: несуществующий ящик будет отвергаться вечно,
// а очередь — расти. После исчерпания попыток письмо переходит в FAILED
// и остаётся в таблице как след для разбирательства.
func (r *OutboxRepo) MarkFailed(ctx context.Context, id uuid.UUID, cause string, retryIn time.Duration, maxAttempts int16) error {
	const q = `
		update email_outbox
		   set status = case when attempts >= $4 then 'FAILED' else 'PENDING' end,
		       next_attempt_at = now() + $3::interval,
		       last_error = $2
		 where id = $1`

	if _, err := r.db.Exec(ctx, q, id, cause, retryIn.String(), maxAttempts); err != nil {
		return fmt.Errorf("mark failed: %w", mapError(err))
	}
	return nil
}
