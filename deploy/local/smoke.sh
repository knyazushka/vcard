#!/usr/bin/env bash
# Сквозная проверка локального прод-стенда через Caddy по HTTPS: всё,
# что на сервере зависит от окружения, — cookie, S3, почта, CORS, лимиты,
# метрики.
#
#   bash deploy/local/smoke.sh
#
# Повторный прогон — не раньше чем через минуту: проверка лимитов
# исчерпывает их, и следующий прогон начнёт с отказов.
set -u

root=$(cd "$(dirname "$0")/../.." && pwd)
work="$root/var/local-stack/smoke"
mkdir -p "$work" && cd "$work"

B=https://localhost:8443/api/v1
PW='Correct-Horse-Battery-9'
ADMIN="admin-$RANDOM$RANDOM@example.com"
cp "$root/internal/render/testdata/preview_minimal.png" avatar.png
# Тела с кириллицей — из файла: аргументы curl в Git Bash теряют UTF-8.
printf '{"name":"Рога и копыта"}' > company.json
rm -f jar.txt

pass=0 fail=0
ok()  { echo "  OK    $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL  $*"; fail=$((fail + 1)); }

# req МЕТОД URL [аргументы curl] — тело в body.json, на выходе код ответа.
req() { local m=$1 u=$2; shift 2; curl -sk -o body.json -w '%{http_code}' -X "$m" "$u" "$@"; }
# field ИМЯ — первое строковое или числовое поле с таким именем в body.json.
field() { grep -o "\"$1\":\"\?[^\",}]*" body.json | head -1 | sed -E 's/^"[^"]+":"?//'; }
expect() {
	if [[ "$1" == "$2" ]]; then ok "$3"; else bad "$3: $1, ждали $2"; head -c 300 body.json; echo; fi
}

echo "== вход и cookie"
code=$(req POST $B/auth/register -c jar.txt -H 'Content-Type: application/json' \
	-d "{\"email\":\"$ADMIN\",\"password\":\"$PW\"}")
expect "$code" 201 "регистрация"
grep -q vcard_refresh jar.txt && ok "refresh-cookie выставлена" || bad "нет refresh-cookie"
code=$(req POST $B/auth/refresh -b jar.txt -c jar.txt)
expect "$code" 200 "обмен refresh-cookie на access-токен"
AUTH=(-H "Authorization: Bearer $(field accessToken)")

echo "== компания, профиль, S3"
code=$(req POST $B/companies "${AUTH[@]}" -H 'Content-Type: application/json' --data-binary @company.json)
expect "$code" 201 "компания с кириллицей в названии"
req GET $B/auth/me "${AUTH[@]}" >/dev/null
CID=$(field companyId) PID=$(field profileId)
[[ -n "$PID" ]] && ok "черновик профиля создан" || bad "нет profileId"

code=$(req PUT $B/profiles/$PID/avatar "${AUTH[@]}" -F "file=@avatar.png;type=image/png")
expect "$code" 200 "аватар загружен"
url=$(field url)
headers=$(curl -sk -o /dev/null -D - "$url" | tr -d '\r')
grep -q '^HTTP/[0-9.]* 200' <<<"$headers" && grep -qi '^cache-control:.*immutable' <<<"$headers" \
	&& grep -qi '^x-content-type-options: nosniff' <<<"$headers" \
	&& ok "аватар отдаётся через /files из бакета" || { bad "аватар через /files"; echo "$headers"; }
code=$(curl -sk -o /dev/null -w '%{http_code}' https://localhost:8443/files/avatars/none.png)
expect "$code" 404 "несуществующий файл — 404"

code=$(req GET $B/profiles/$PID/card.png "${AUTH[@]}")
expect "$code" 200 "карточка нарисована"
[[ "$(head -c 4 body.json | tail -c 3)" == "PNG" ]] && ok "карточка — PNG" || bad "карточка не PNG"
code=$(req GET $B/profiles/$PID/card.png "${AUTH[@]}")
expect "$code" 200 "карточка из кэша"

echo "== приглашение и почта"
before=$(curl -s http://localhost:8026/api/v1/messages | grep -o '"total":[0-9]*' | cut -d: -f2)
code=$(req POST $B/companies/$CID/invitations "${AUTH[@]}" -H 'Content-Type: application/json' \
	-d '{"email":"employee@example.com"}')
expect "$code" 201 "приглашение создано"
for _ in $(seq 1 15); do
	now=$(curl -s http://localhost:8026/api/v1/messages | grep -o '"total":[0-9]*' | cut -d: -f2)
	[[ "${now:-0}" -gt "${before:-0}" ]] && break
	sleep 1
done
[[ "${now:-0}" -gt "${before:-0}" ]] && ok "письмо дошло до Mailpit" || bad "письма нет в Mailpit"

echo "== CORS"
h=$(curl -sk -o /dev/null -D - -X OPTIONS $B/auth/refresh -H 'Origin: http://localhost:3000' \
	-H 'Access-Control-Request-Method: POST' | tr -d '\r')
grep -q '^HTTP/[0-9.]* 204' <<<"$h" && grep -qi '^access-control-allow-origin: http://localhost:3000' <<<"$h" \
	&& grep -qi '^access-control-allow-credentials: true' <<<"$h" \
	&& ok "preflight фронта: origin и credentials" || { bad "preflight фронта"; echo "$h"; }
h=$(curl -sk -o /dev/null -D - -X OPTIONS $B/auth/refresh -H 'Origin: https://evil.example.org' \
	-H 'Access-Control-Request-Method: POST' | tr -d '\r')
grep -qi '^access-control-allow-origin' <<<"$h" && bad "чужой origin получил разрешение" || ok "чужой origin без разрешений"

echo "== лимиты"
codes=""
for _ in $(seq 1 11); do
	codes+="$(req POST $B/auth/login -D headers.txt -H 'Content-Type: application/json' \
		-d "{\"email\":\"$ADMIN\",\"password\":\"wrong-password\"}") "
done
[[ "$codes" == "401 401 401 401 401 401 401 401 401 401 429 " ]] \
	&& ok "вход: 10 попыток, 11-я — 429" || bad "строгий лимит входа: $codes"
grep -qi '^retry-after:' headers.txt && ok "есть Retry-After" || bad "нет Retry-After"
[[ "$(field code)" == "rate_limited" && -n "$(field requestId)" ]] \
	&& ok "тело 429 — Error с rate_limited" || bad "тело 429: $(cat body.json)"
code=$(req POST $B/companies/$CID/invitations "${AUTH[@]}" -H 'Content-Type: application/json' \
	-d '{"email":"employee2@example.com"}')
expect "$code" 201 "лимит входа не задел приглашения"

# Один процесс curl и параллельные запросы: иначе запросы идут медленнее,
# чем пополняется корзина, и лимит не достигается.
args=()
for _ in $(seq 1 130); do args+=(-o /dev/null "$B/auth/me"); done
n429=$(curl -sk --parallel --parallel-max 10 -w '%{http_code}\n' "${AUTH[@]}" "${args[@]}" | grep -c 429)
[[ "$n429" -gt 0 ]] && ok "общий лимит: $n429 отказов из 130 запросов" || bad "общий лимит не сработал"

echo "== метрики"
metrics=$(curl -s http://127.0.0.1:9091/metrics)
for m in 'vcard_http_requests_total{code="429",operation="login"}' 'vcard_card_render_duration_seconds_count' \
	'vcard_outbox_sent_total' 'vcard_db_pool_connections'; do
	grep -qF "$m" <<<"$metrics" && ok "есть $m" || bad "нет $m"
done

echo
echo "итого: $pass OK, $fail FAIL"
[[ $fail -eq 0 ]]
