-- +goose Up

-- Служебные адреса, которые не должен занять сотрудник: они пересекаются
-- с маршрутами фронта и API. Лежат в отдельной таблице, а не в константе
-- в коде, чтобы список пополнялся без релиза.
create table reserved_slugs (
    slug       citext primary key,
    created_at timestamptz not null default now()
);

insert into reserved_slugs (slug)
values ('admin'), ('api'), ('app'), ('auth'), ('employee'), ('login'),
       ('logout'), ('register'), ('invite'), ('invitations'), ('profile'),
       ('profiles'), ('company'), ('companies'), ('settings'), ('static'),
       ('assets'), ('public'), ('health'), ('healthz'), ('readyz'),
       ('metrics'), ('robots'), ('sitemap'), ('favicon'), ('www'), ('me');

create table profiles (
    id                    uuid primary key default gen_random_uuid(),
    user_id               uuid        not null references users (id) on delete restrict,
    company_id            uuid        not null references companies (id) on delete cascade,

    -- Уникальность глобальная и БЕЗУСЛОВНАЯ: по ТЗ блокировка страницы
    -- адрес не освобождает, значит частичного индекса «только у активных»
    -- здесь быть не может. citext — чтобы /employee/Laura и /employee/laura
    -- вели на одну страницу, а не создавали две.
    slug                  citext      not null unique,

    status                text        not null default 'DRAFT'
        check (status in ('DRAFT', 'PUBLISHED', 'BLOCKED', 'ARCHIVED')),

    full_name             text check (char_length(full_name) <= 120),

    -- Ключи в хранилище, не URL.
    avatar_key            text,
    -- Кроп метаданными: исходник остаётся целым и область можно переиграть,
    -- не заставляя человека загружать фотографию заново.
    avatar_crop_x         integer check (avatar_crop_x >= 0),
    avatar_crop_y         integer check (avatar_crop_y >= 0),
    avatar_crop_size      integer check (avatar_crop_size > 0),

    -- Уже санитизированный HTML. Санитизация на записи, а не на чтении:
    -- иначе обновление санитайзера молча меняет вид давно сохранённых страниц.
    about_html            text check (char_length(about_html) <= 8000),
    -- Тот же текст без разметки — для поиска и для og:description.
    about_text            text,

    -- Без ON DELETE SET NULL: обнулять должность живого сотрудника нельзя,
    -- он об этом не узнает и увидит дыру на своей визитке. Удаление
    -- должности из справочника переносит её название в position_custom —
    -- этим занимается триггер ниже.
    position_id           uuid references positions (id),
    position_custom       text check (char_length(position_custom) <= 120),

    phone                 text check (char_length(phone) <= 32),
    whatsapp              text check (char_length(whatsapp) <= 32),
    telegram              text check (char_length(telegram) <= 64),

    show_company_contacts boolean     not null default false,

    -- Имя файла карточки = хэш от данных профиля и версии рендерера.
    -- Меняется сам при любой правке, поэтому инвалидация кэша не нужна.
    card_key              text,
    card_hash             text,

    created_at            timestamptz not null default now(),
    updated_at            timestamptz not null default now(),

    -- Должность либо из справочника, либо своя — но не обе сразу.
    constraint profiles_position_xor
        check (position_id is null or position_custom is null),

    -- Кроп задаётся целиком или не задаётся вовсе.
    constraint profiles_crop_complete check (
        (avatar_crop_x is null and avatar_crop_y is null and avatar_crop_size is null)
        or (avatar_crop_x is not null and avatar_crop_y is not null and avatar_crop_size is not null)
    ),

    -- Один профиль на человека в компании. Состоять в нескольких компаниях
    -- модель разрешает — у каждой будет своя визитка со своим адресом.
    constraint profiles_user_company_uq unique (user_id, company_id)
);

create index profiles_company_idx on profiles (company_id);
create index profiles_user_idx on profiles (user_id);

create trigger profiles_set_updated_at
    before update on profiles
    for each row execute function set_updated_at();

-- Резерв проверяется базой, а не только приложением: адрес может прийти
-- и из миграции, и из ручного INSERT при поддержке.
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

create trigger profiles_slug_not_reserved
    before insert or update of slug on profiles
    for each row execute function assert_slug_not_reserved();

-- Теги профиля.
--
-- Потолок «не больше трёх» задан структурой, а не триггером: sort_order
-- входит в первичный ключ и ограничен диапазоном 0..2, поэтому четвёртая
-- строка физически не вставляется. Требование «ровно три для публикации» —
-- уже правило домена: у черновика тегов может быть меньше.
create table profile_tags (
    profile_id  uuid     not null references profiles (id) on delete cascade,
    sort_order  smallint not null check (sort_order between 0 and 2),
    -- Тег либо из справочника компании, либо введённый вручную.
    tag_id      uuid references tags (id),
    custom_title text check (char_length(custom_title) between 1 and 75),
    primary key (profile_id, sort_order),

    -- Ровно один из двух источников. Обе колонки пустыми быть не могут:
    -- удаление тега из справочника переводит ссылку в пользовательский
    -- вариант, а не оставляет пустое место на чужой визитке.
    constraint profile_tags_source_xor
        check ((tag_id is null) <> (custom_title is null))
);

-- Один и тот же справочный тег не должен попасть в профиль дважды.
create unique index profile_tags_unique_tag
    on profile_tags (profile_id, tag_id) where tag_id is not null;

-- Удаление записи справочника не должно ни ломать чужие визитки, ни падать
-- по внешнему ключу. Оба триггера материализуют название в профиль ДО того,
-- как строка исчезнет: ссылка превращается в пользовательское значение.
--
-- BEFORE DELETE, а не ON DELETE SET NULL, потому что SET NULL обнулил бы
-- одну колонку из пары и нарушил XOR-констрейнт. Внешний ключ без ON DELETE
-- проверяется в конце оператора, к этому моменту ссылок уже не остаётся.
-- +goose StatementBegin
create or replace function demote_position_to_custom() returns trigger as $$
begin
    update profiles
       set position_custom = old.title,
           position_id     = null
     where position_id = old.id;
    return old;
end;
$$ language plpgsql;
-- +goose StatementEnd

create trigger positions_demote_before_delete
    before delete on positions
    for each row execute function demote_position_to_custom();

-- +goose StatementBegin
create or replace function demote_tag_to_custom() returns trigger as $$
begin
    update profile_tags
       set custom_title = old.title,
           tag_id       = null
     where tag_id = old.id;
    return old;
end;
$$ language plpgsql;
-- +goose StatementEnd

create trigger tags_demote_before_delete
    before delete on tags
    for each row execute function demote_tag_to_custom();

-- +goose Down
drop trigger if exists tags_demote_before_delete on tags;
drop function if exists demote_tag_to_custom();
drop trigger if exists positions_demote_before_delete on positions;
drop function if exists demote_position_to_custom();
drop table if exists profile_tags;
drop trigger if exists profiles_slug_not_reserved on profiles;
drop function if exists assert_slug_not_reserved();
drop table if exists profiles;
drop table if exists reserved_slugs;
