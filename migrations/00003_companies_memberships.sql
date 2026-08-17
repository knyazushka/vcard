-- +goose Up

create table companies (
    id         uuid primary key default gen_random_uuid(),
    name       text        not null check (char_length(name) between 1 and 200),
    -- Ключ в хранилище, НЕ URL. Переезд с локального диска на S3 тогда
    -- меняет один адаптер и переменную окружения, а не содержимое таблицы.
    logo_key   text,
    address    text check (char_length(address) <= 500),
    status     text        not null default 'ACTIVE' check (status in ('ACTIVE', 'SUSPENDED')),
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create trigger companies_set_updated_at
    before update on companies
    for each row execute function set_updated_at();

-- Телефоны компании: упорядоченный список, который API заменяет целиком.
-- sort_order в первичном ключе с CHECK 0..9 задаёт и порядок, и потолок
-- в десять номеров — отдельного триггера на количество не нужно.
create table company_phones (
    company_id uuid     not null references companies (id) on delete cascade,
    sort_order smallint not null check (sort_order between 0 and 9),
    value      text     not null check (char_length(value) <= 32),
    label      text check (char_length(label) <= 40),
    primary key (company_id, sort_order)
);

-- Членство. Роль — свойство связки, а не пользователя: один человек может
-- быть администратором в одной компании и рядовым сотрудником в другой.
--
-- text + CHECK вместо enum-типа: значения в enum добавляются легко, а вот
-- переименовать или убрать — только пересозданием типа со всеми зависимостями.
create table memberships (
    user_id    uuid        not null references users (id) on delete cascade,
    company_id uuid        not null references companies (id) on delete cascade,
    role       text        not null check (role in ('ADMIN', 'EMPLOYEE')),
    created_at timestamptz not null default now(),
    primary key (user_id, company_id)
);

create index memberships_company_idx on memberships (company_id);
-- Покрывающий индекс для проверки «остался ли администратор».
create index memberships_company_admin_idx on memberships (company_id) where role = 'ADMIN';

-- Компания без администратора неуправляема и чинится только руками в SQL,
-- поэтому проверка живёт в базе, а не в use-case: два одновременных
-- понижения по отдельности выглядят допустимыми и вместе оставляют компанию
-- без владельца.
--
-- CONSTRAINT TRIGGER DEFERRABLE — чтобы внутри одной транзакции можно было
-- передать администрирование: снять роль с одного и выдать другому в любом
-- порядке. Проверка сработает один раз, на коммите.
-- +goose StatementBegin
create or replace function assert_company_has_admin() returns trigger as $$
declare
    cid uuid := coalesce(old.company_id, new.company_id);
begin
    -- Компанию удалили целиком — администратор ей больше не нужен.
    if not exists (select 1 from companies where id = cid) then
        return null;
    end if;

    if not exists (
        select 1 from memberships
        where company_id = cid and role = 'ADMIN'
    ) then
        raise exception 'company % would be left without an admin', cid
            using errcode = 'raise_exception';
    end if;

    return null;
end;
$$ language plpgsql;
-- +goose StatementEnd

create constraint trigger memberships_keep_admin
    after update or delete on memberships
    deferrable initially deferred
    for each row execute function assert_company_has_admin();

-- +goose Down
drop trigger if exists memberships_keep_admin on memberships;
drop function if exists assert_company_has_admin();
drop table if exists memberships;
drop table if exists company_phones;
drop table if exists companies;
