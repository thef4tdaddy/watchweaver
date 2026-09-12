#!/usr/bin/env bash
# Exercise the production image from a separate bot network and the host.
set -euo pipefail
image=${1:-watchweaver:test}
secret_dir=$(mktemp -d)
web_password=ci-web-password-independent-0123456789
bot_token=ci-bot-token-independent-01234567890
cleanup() {
  docker rm -f ww-bot-boundary >/dev/null 2>&1 || true
  docker network rm ww-boundary-web ww-boundary-bot >/dev/null 2>&1 || true
  rm -rf "$secret_dir"
}
trap cleanup EXIT
printf '%s' "$web_password" > "$secret_dir/web"
printf '%s' "$bot_token" > "$secret_dir/bot"
chmod 755 "$secret_dir"
chmod 444 "$secret_dir/web" "$secret_dir/bot"
docker network create --subnet 172.29.81.0/24 ww-boundary-web
docker network create --internal --subnet 172.29.82.0/24 ww-boundary-bot
docker create --name ww-bot-boundary --network ww-boundary-web --ip 172.29.81.2 \
  -p 127.0.0.1:18081:8080 -v "$secret_dir:/run/secrets:ro" \
  -e WATCHWEAVER_LISTEN_ADDR=172.29.81.2:8080 \
  -e WATCHWEAVER_BOT_LISTEN_ADDR=172.29.82.2:8081 \
  -e WATCHWEAVER_BOT_TOKEN_FILE=/run/secrets/bot \
  -e WATCHWEAVER_WEB_PASSWORD_FILE=/run/secrets/web \
  -e WATCHWEAVER_BOT_USER_IDS=123 \
  -e WATCHWEAVER_PUBLIC_URL=https://ww.example "$image"
docker network connect --ip 172.29.82.2 ww-boundary-bot ww-bot-boundary
docker start ww-bot-boundary
for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:18081/readyz >/dev/null; then break; fi
  sleep 1
done
curl -fsS http://127.0.0.1:18081/readyz
# Every documented browser path must serve the built SPA on a fresh request.
for path in '/inbox?type=movie&q=title' '/history?page=2&q=title' /media/1 /tasks/1 /letterboxd /serializd /status /settings/trakt /settings/jellyfin /settings/discord /settings/preferences /movies /tv; do
  curl -fsS -u "watchweaver:$web_password" "http://127.0.0.1:18081$path" | grep -q 'id="root"'
done
# A reachable host-published web API still rejects the bot credential.
for path in /api/settings /api/integrations/discord-bot /api/letterboxd/export; do
  test "$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $bot_token" "http://127.0.0.1:18081$path")" = 401
  test "$(curl -s -o /dev/null -w '%{http_code}' -u "watchweaver:$bot_token" "http://127.0.0.1:18081$path")" = 401
done
# Use the production image's wget as a distinct client on the private network.
docker run --rm --network ww-boundary-bot --entrypoint sh "$image" -ec '
  token=$1
  if wget -q -T 3 -O /dev/null http://172.29.82.2:8080/api/settings; then exit 1; fi
  wget -q -T 5 -O - --header="Authorization: Bearer $token" --header="X-Discord-User-ID: 123" --header="Content-Type: application/json" --post-data='"'"'{"protocol_version":"1"}'"'"' http://172.29.82.2:8081/api/bot/v1/handshake | grep -q '"'"'"connected":true'"'"'
  if wget -q -T 5 -O /dev/null --header="Authorization: Bearer $token" --header="X-Discord-User-ID: 123" http://172.29.82.2:8081/api/settings; then exit 1; fi
' sh "$bot_token"
echo 'Production SPA routes, authenticated handshake, and bot/admin boundary passed.'
