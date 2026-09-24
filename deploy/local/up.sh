#!/bin/sh
# Поднимает локальный прод-стенд: образ из рабочей копии и тот же
# deploy/compose.yaml, что на сервере, в том же порядке, что deploy.sh.
# Отличия от прода — только в compose.override.yaml рядом.
#
#   sh deploy/local/up.sh
#
# Другой бинарь compose (например, свежий docker-compose.exe при старом
# Docker Desktop): COMPOSE=/путь/к/docker-compose sh deploy/local/up.sh
set -eu

# Git Bash на Windows переписывает аргументы вида /etc/... в пути Windows,
# и команды внутри контейнеров получают несуществующие файлы.
export MSYS_NO_PATHCONV=1

root=$(cd "$(dirname "$0")/../.." && pwd)
stand="$root/var/local-stack"
compose=${COMPOSE:-docker compose}
dc() { $compose -p vcard-local "$@"; }

# env_file с format: raw появился в Docker Compose 2.30. Старый compose
# падает на разборе с невнятным «env_file.0 must be a string».
version=$($compose version --short | sed 's/^v//')
major=${version%%.*}
minor=${version#*.}
minor=${minor%%.*}
if [ "$major" -lt 2 ] || { [ "$major" -eq 2 ] && [ "$minor" -lt 30 ]; }; then
	echo "нужен Docker Compose 2.30+, установлен $version — обновите Docker Desktop" >&2
	exit 1
fi

# Контекст сборки — относительным путём: при выключенной подмене путей
# docker.exe на Windows не понял бы /c/Users/...
(cd "$root" && docker build -t vcard:local .)

# Каталог стенда собирается заново: он всегда соответствует deploy/
# в рабочей копии. Данные живут в томах docker, а не здесь.
rm -rf "$stand"
mkdir -p "$stand"
cp -R "$root/deploy/." "$stand/"
rm -rf "$stand/local"
cp "$root/deploy/local/compose.override.yaml" "$root/deploy/local/app.env" \
	"$root/deploy/local/grafana.env" "$stand/"
cp "$root/deploy/local/compose.env" "$stand/.env"
cd "$stand"

dc up --detach --wait postgres minio mailpit

# Бакет на сервере создаётся в панели хранилища, здесь — клиентом MinIO.
# Сервер принимает запросы не сразу после старта контейнера.
attempt=0
until dc exec -T minio mc alias set local http://localhost:9000 vcard-local vcard-local-secret >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	[ "$attempt" -ge 15 ] && { echo "MinIO не поднялся" >&2; exit 1; }
	sleep 1
done
dc exec -T minio mc mb --ignore-existing local/vcard >/dev/null

dc run --rm migrator
dc up --detach --remove-orphans
dc exec -T caddy caddy reload --config /etc/caddy/Caddyfile
dc kill --signal HUP prometheus >/dev/null
dc restart grafana >/dev/null

attempt=0
until curl --silent --fail --max-time 2 http://127.0.0.1:9091/readyz >/dev/null; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 30 ]; then
		echo "api не стал готов за минуту" >&2
		dc logs --tail 100 api >&2
		exit 1
	fi
	sleep 2
done

cat <<EOF

Стенд готов.
  API       https://localhost:8443/api/v1   (сертификат самоподписанный: curl -k)
  Grafana   http://localhost:3030           admin / local-admin, дашборд vcard
  Mailpit   http://localhost:8026           пойманные письма
  Пробы     http://127.0.0.1:9091/readyz

Проверка:    bash deploy/local/smoke.sh
Трафик:      bash deploy/local/traffic.sh [минут]
Остановить:  sh deploy/local/down.sh
EOF
