-- +goose Up

-- Триггеры поднимали исключения с кодом по умолчанию (P0001), и различить
-- «компания осталась бы без администратора» от «адрес зарезервирован» можно
-- было только разбором текста сообщения — то есть строкой, которая меняется
-- при первой же правке формулировки.
--
-- Собственные SQLSTATE решают это раз и навсегда: приложение сопоставляет код,
-- а не текст. Класс VC выбран свободным — стандарт его не занимает.
--
--   VC001 — компания осталась бы без администратора
--   VC002 — адрес визитки зарезервирован

-- +goose StatementBegin
create or replace function assert_company_has_admin() returns trigger as $$
declare
    cid uuid := coalesce(old.company_id, new.company_id);
begin
    if not exists (select 1 from companies where id = cid) then
        return null;
    end if;

    if not exists (
        select 1 from memberships
        where company_id = cid and role = 'ADMIN'
    ) then
        raise exception 'company % would be left without an admin', cid
            using errcode = 'VC001';
    end if;

    return null;
end;
$$ language plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
create or replace function assert_slug_not_reserved() returns trigger as $$
begin
    if exists (select 1 from reserved_slugs where slug = new.slug) then
        raise exception 'slug % is reserved', new.slug
            using errcode = 'VC002';
    end if;
    return new;
end;
$$ language plpgsql;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
create or replace function assert_company_has_admin() returns trigger as $$
declare
    cid uuid := coalesce(old.company_id, new.company_id);
begin
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

-- +goose StatementBegin
create or replace function assert_slug_not_reserved() returns trigger as $$
begin
    if exists (select 1 from reserved_slugs where slug = new.slug) then
        raise exception 'slug % is reserved', new.slug
            using errcode = 'raise_exception';
    end if;
    return new;
end;
$$ language plpgsql;
-- +goose StatementEnd
