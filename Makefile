.PHONY: api-lint api-bundle api-gen api-check build test run \
        db-up db-down migrate-up migrate-down migrate-status migrate-verify

SPEC_DIR := api/openapi
BUNDLE   := $(SPEC_DIR)/openapi.bundled.yaml
GEN_DIR  := internal/gen/openapi

# Строка подключения для локальной разработки. В проде приходит из окружения
# и в репозитории её нет — это фактор III.
DB_DSN ?= postgres://vcard:vcard@localhost:5433/vcard?sslmode=disable

# Спека — источник истины. Порядок обязателен: линт → бандл → кодоген.
# ogen не разрешает внешние $ref, поэтому многофайловая спека сначала
# сшивается в один документ; он же отдаётся фронту для openapi-typescript.

api-lint:
	npx --yes @redocly/cli@latest lint --config $(SPEC_DIR)/redocly.yaml

api-bundle: api-lint
	npx --yes @redocly/cli@latest bundle vcard \
		--config $(SPEC_DIR)/redocly.yaml --output $(BUNDLE)

api-gen: api-bundle
	go tool ogen --target $(GEN_DIR) --package openapi --clean $(BUNDLE)

# Для CI: падает, если сгенерированный код разошёлся со спекой.
# Без этой проверки contract-first держится на честном слове.
api-check: api-gen
	git diff --exit-code -- $(BUNDLE) $(GEN_DIR)

build:
	go build ./...

test:
	go test ./...

# Запуск для разработки.
#
# Конфигурация читается только из окружения (фактор III), и загрузчика .env
# в приложении нет намеренно: в проде переменные ставит среда исполнения,
# а лишняя зависимость ради удобства разработки живёт бы в проде тоже.
# Поэтому .env экспортирует make, а не процесс: файл остаётся удобством
# оболочки, приложение по-прежнему знает только про окружение.
run:
	@test -f .env || { echo "нет .env — скопируйте .env.example"; exit 1; }
	set -a; . ./.env; set +a; go run ./cmd/api

# Сгенерированный ogen код из проверки исключён: правится он только
# перегенерацией, а замечания к нему всё равно некому адресовать.
lint:
	go tool golangci-lint run ./cmd/... ./internal/... ./migrations/...

lint-fix:
	go tool golangci-lint run --fix ./cmd/... ./internal/... ./migrations/...

# --- база -------------------------------------------------------------------
# Миграции — отдельный шаг релиза, а не действие при старте приложения:
# иначе три пода на выкатке дерутся за схему.

db-up:
	docker compose up -d postgres mailpit

db-down:
	docker compose down

migrate-up:
	go tool goose -dir migrations postgres "$(DB_DSN)" up

migrate-down:
	go tool goose -dir migrations postgres "$(DB_DSN)" down

migrate-status:
	go tool goose -dir migrations postgres "$(DB_DSN)" status

# Прогоняет всю цепочку вверх, затем полностью вниз и снова вверх.
# Ловит то, что обычный `up` не ловит: неработающие Down-секции, порядок
# удаления объектов, зависимости триггеров и функций.
migrate-verify:
	go tool goose -dir migrations postgres "$(DB_DSN)" up
	go tool goose -dir migrations postgres "$(DB_DSN)" reset
	go tool goose -dir migrations postgres "$(DB_DSN)" up
