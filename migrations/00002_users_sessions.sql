-- +goose Up

create table users (
    id             uuid primary key default gen_random_uuid(),
    email          citext      not null unique,
    email_verified boolean     not null default false,
    password_hash  text        not null,
    created_at     timestamptz not null default now(),
    updated_at     timestamptz not null default now(),

    -- 254 — предел из RFC 5321 и то же число, что в maxLength спеки.
    -- CHECK, а не varchar(254): уменьшить varchar можно только переписав
    -- таблицу под эксклюзивной блокировкой, а CHECK меняется через
    -- NOT VALID + VALIDATE без простоя.
    constraint users_email_len check (char_length(email) <= 254)
);

create trigger users_set_updated_at
    before update on users
    for each row execute function set_updated_at();

-- Сессии = refresh-токены.
--
-- Токен opaque, а не JWT: отозвать JWT невозможно, а отзыв — единственное,
-- ради чего refresh нужен. В базе лежит только sha256, сам токен существует
-- в cookie у клиента.
--
-- id первой сессии семейства попадает в access-токен как `sid`. Ротация
-- создаёт новую строку с тем же family_id, поэтому access переживает
-- обмен refresh и остаётся привязан к цепочке, а не к экземпляру.
create table sessions (
    id         uuid primary key default gen_random_uuid(),
    family_id  uuid        not null,
    user_id    uuid        not null references users (id) on delete cascade,
    token_hash bytea       not null unique,
    issued_at  timestamptz not null default now(),
    expires_at timestamptz not null,
    -- Проставляется в момент обмена. Предъявление токена с непустым used_at
    -- означает, что копию использовали дважды: легитимный клиент так не может,
    -- он уже получил новый. Это признак кражи — гасится всё семейство.
    used_at    timestamptz,
    revoked_at timestamptz,
    user_agent text,
    ip         inet
);

create index sessions_family_idx on sessions (family_id);
create index sessions_user_idx on sessions (user_id) where revoked_at is null;
-- Для фоновой чистки протухших.
create index sessions_expires_idx on sessions (expires_at) where revoked_at is null;

-- +goose Down
drop table if exists sessions;
drop table if exists users;
