-- +goose Up

-- Транзакционный outbox.
--
-- Письмо кладётся сюда в той же транзакции, что и приглашение: либо есть
-- и то и другое, либо ничего. Синхронная отправка прямо из обработчика
-- означала бы, что недоступность почтового провайдера роняет создание
-- приглашения, а падение между COMMIT и отправкой оставляет приглашение
-- без письма.
--
-- Отдельная очередь (Redis, RabbitMQ) для этого не нужна: SELECT ...
-- FOR UPDATE SKIP LOCKED даёт конкурентную выборку без блокировок
-- на таблице, и одним backing service меньше.
create table email_outbox (
    id              uuid primary key default gen_random_uuid(),
    kind            text        not null,
    to_email        citext      not null,
    payload         jsonb       not null,
    status          text        not null default 'PENDING'
        check (status in ('PENDING', 'SENT', 'FAILED')),
    attempts        smallint    not null default 0,
    -- Момент следующей попытки; воркер берёт только созревшие.
    -- Экспоненциальный бэкофф выставляет его при неудаче.
    next_attempt_at timestamptz not null default now(),
    last_error      text,
    created_at      timestamptz not null default now(),
    sent_at         timestamptz
);

-- Рабочий индекс воркера: только неотправленные, только созревшие.
-- Частичный, поэтому не растёт вместе с историей отправленных писем.
create index email_outbox_due_idx
    on email_outbox (next_attempt_at)
    where status = 'PENDING';

-- +goose Down
drop table if exists email_outbox;
