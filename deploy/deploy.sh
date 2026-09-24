#!/bin/sh
# Выкатка на сервере. Запускается из CI по SSH, когда рядом уже лежат
# свежие compose.yaml, Caddyfile, .env и app.env.
#
#   deploy.sh <пользователь реестра>   токен реестра — на stdin
#
# Токен — GITHUB_TOKEN конкретного запуска CI: живёт, пока идёт job,
# и на сервере не хранится.
set -eu

cd "$(dirname "$0")"
chmod 600 app.env grafana.env

registry_user=$1
docker login ghcr.io --username "$registry_user" --password-stdin >/dev/null
trap 'docker logout ghcr.io >/dev/null 2>&1 || true' EXIT

docker compose pull --quiet

# Сначала схема, потом код. Миграции обязаны быть совместимы с прежней
# версией приложения: она ещё работает, пока идёт этот шаг.
docker compose run --rm migrator

docker compose up --detach --remove-orphans

# Caddy и Prometheus не перечитывают конфиги сами, а `up` не пересоздаёт
# контейнер, если изменился только примонтированный файл. Битый конфиг
# оба отвергают и продолжают работать на прежнем; Caddy при этом ещё
# и роняет выкатку, Prometheus — только пишет ошибку в свой лог.
docker compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile
docker compose kill --signal HUP prometheus
# Grafana читает дашборды из файлов только при старте. Перезапуск —
# секунд десять без графиков; метрики тем временем собирает Prometheus.
docker compose restart grafana

# `up` возвращается, как только контейнер запущен, а не когда процесс
# готов. Готовность — это /readyz: он же проверяет базу.
attempt=0
until curl --silent --fail --max-time 2 http://127.0.0.1:9090/readyz >/dev/null; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 30 ]; then
		echo "api не стал готов за минуту" >&2
		docker compose logs --tail 100 api >&2
		exit 1
	fi
	sleep 2
done

# Прежние образы больше не нужны: откат — это выкатка старого тега,
# и он скачивается из реестра заново.
docker image prune --all --force >/dev/null

echo "выкачен $(grep '^IMAGE=' .env | cut -d= -f2-)"
