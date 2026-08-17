-- +goose Up

-- citext даёт регистронезависимое сравнение на уровне типа. Без него
-- уникальность почты пришлось бы держать индексом по lower(email),
-- а нормализацию — дисциплиной в коде: любой забытый lower() создаёт
-- второго пользователя с тем же адресом.
create extension if not exists citext;

-- Общий триггер для updated_at. Прикладной код может забыть выставить
-- поле, база — нет.
-- +goose StatementBegin
create or replace function set_updated_at() returns trigger as $$
begin
    new.updated_at = now();
    return new;
end;
$$ language plpgsql;
-- +goose StatementEnd

-- +goose Down
drop function if exists set_updated_at();
drop extension if exists citext;
