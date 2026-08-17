-- +goose Up

-- Приглашение — единственный способ попасть в компанию.
create table invitations (
    id          uuid primary key default gen_random_uuid(),
    company_id  uuid        not null references companies (id) on delete cascade,
    email       citext      not null check (char_length(email) <= 254),
    role        text        not null default 'EMPLOYEE' check (role in ('ADMIN', 'EMPLOYEE')),

    -- sha256 от токена, а не сам токен. Токен — 32 байта из crypto/rand —
    -- существует только в письме: утечка дампа базы не даёт вступить
    -- ни в одну компанию.
    token_hash  bytea       not null unique,

    invited_by  uuid        not null references users (id) on delete restrict,
    status      text        not null default 'PENDING'
        check (status in ('PENDING', 'ACCEPTED', 'REVOKED')),

    expires_at  timestamptz not null,
    accepted_at timestamptz,
    accepted_by uuid references users (id) on delete set null,
    last_sent_at timestamptz,
    created_at  timestamptz not null default now(),

    -- Принятое приглашение обязано знать, кем и когда.
    constraint invitations_accepted_consistent check (
        (status = 'ACCEPTED') = (accepted_at is not null and accepted_by is not null)
    )
);

-- EXPIRED в статусах намеренно нет: истечение вычисляется при чтении
-- (expires_at < now()), иначе состояние строки зависело бы от того,
-- жив ли фоновый воркер.

-- Частичный уникальный индекс: одно действующее приглашение на адрес
-- в компании. Отозванное или принятое не мешает пригласить заново.
create unique index invitations_pending_uq
    on invitations (company_id, email) where status = 'PENDING';

create index invitations_company_idx on invitations (company_id, created_at desc);

-- +goose Down
drop table if exists invitations;
