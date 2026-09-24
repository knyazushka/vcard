#!/bin/sh
# Останавливает локальный прод-стенд и удаляет его данные: базу, бакет,
# метрики. Окружение разработки из docker-compose.yml не трогает —
# у стенда своё имя проекта.
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
compose=${COMPOSE:-docker compose}

cd "$root/var/local-stack"
$compose -p vcard-local down --volumes --remove-orphans
