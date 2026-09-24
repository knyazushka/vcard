#!/usr/bin/env bash
# Фоновый трафик для дашборда: обычная работа пользователя, раз в минуту —
# всплеск неверных входов (виден лимит), время от времени — приглашения
# (видна почта) и новый аватар (видна отрисовка карточки).
#
#   bash deploy/local/traffic.sh [минут, по умолчанию 15]
set -u

root=$(cd "$(dirname "$0")/../.." && pwd)
work="$root/var/local-stack/traffic"
mkdir -p "$work" && cd "$work"

B=https://localhost:8443/api/v1
E="traffic-$RANDOM$RANDOM@example.com"
PW='Correct-Horse-Battery-9'
cp "$root/internal/render/testdata/preview_minimal.png" avatar.png
printf '{"name":"Рога и копыта"}' > company.json

field() { grep -o "\"$1\":\"[^\"]*" | head -1 | sed -E 's/^"[^"]+":"//'; }

T=$(curl -sk -H 'Content-Type: application/json' -d "{\"email\":\"$E\",\"password\":\"$PW\"}" $B/auth/register | field accessToken)
A=(-H "Authorization: Bearer $T")
curl -sk -o /dev/null "${A[@]}" -H 'Content-Type: application/json' --data-binary @company.json $B/companies
me=$(curl -sk "${A[@]}" $B/auth/me)
CID=$(field companyId <<<"$me") PID=$(field profileId <<<"$me")
curl -sk -o /dev/null -X PUT "${A[@]}" -F "file=@avatar.png;type=image/png" $B/profiles/$PID/avatar

end=$((SECONDS + ${1:-15} * 60)) i=0
echo "трафик до $(date -d "@$(( $(date +%s) + ${1:-15} * 60 ))" +%H:%M 2>/dev/null || echo "окончания"), Ctrl+C — остановить"
while ((SECONDS < end)); do
	i=$((i + 1))
	curl -sk -o /dev/null "${A[@]}" $B/auth/me
	curl -sk -o /dev/null "${A[@]}" $B/profiles/$PID
	curl -sk -o /dev/null "${A[@]}" $B/profiles/$PID/card.png
	((i % 5 == 0)) && curl -sk -o /dev/null https://localhost:8443/.env
	((i % 20 == 0)) && curl -sk -o /dev/null "${A[@]}" -H 'Content-Type: application/json' \
		-d "{\"email\":\"guest-$i@example.com\"}" $B/companies/$CID/invitations
	if ((i % 40 == 0)); then
		for _ in $(seq 1 14); do
			curl -sk -o /dev/null -H 'Content-Type: application/json' \
				-d "{\"email\":\"$E\",\"password\":\"wrong\"}" $B/auth/login
		done
		curl -sk -o /dev/null -X PUT "${A[@]}" -F "file=@avatar.png;type=image/png" \
			-F "cropX=$((i % 7))" -F cropY=0 -F cropSize=100 $B/profiles/$PID/avatar
	fi
	sleep 1
done
