-- +goose Up

-- Справочники должностей и тегов — у каждой компании свои.
--
-- Нормализованное название хранится генерируемым столбцом, а уникальность
-- строится по нему. Так «Golang», «golang» и «  GoLang » не могут попасть
-- в один справочник, и правило нельзя обойти мимо приложения — например,
-- из миграции или psql.

create table positions (
    id         uuid primary key default gen_random_uuid(),
    company_id uuid        not null references companies (id) on delete cascade,
    title      text        not null check (char_length(title) between 1 and 120),
    title_norm text generated always as (
        lower(btrim(regexp_replace(title, '\s+', ' ', 'g')))
    ) stored,
    created_at timestamptz not null default now()
);

create unique index positions_company_title_uq on positions (company_id, title_norm);

create table tags (
    id         uuid primary key default gen_random_uuid(),
    company_id uuid        not null references companies (id) on delete cascade,
    title      text        not null check (char_length(title) between 1 and 75),
    title_norm text generated always as (
        lower(btrim(regexp_replace(title, '\s+', ' ', 'g')))
    ) stored,
    created_at timestamptz not null default now()
);

create unique index tags_company_title_uq on tags (company_id, title_norm);

-- +goose Down
drop table if exists tags;
drop table if exists positions;
