#!/usr/bin/env bash
set -euo pipefail

image="${1:?published image reference is required}"
version="${2:?expected application version is required}"
platform="${3:-linux/amd64}"
previous_image="${4:-}"
suffix="${platform##*/}"
container="watchweaver-published-${suffix}"
volume="watchweaver-published-${suffix}"

cleanup() {
  docker rm -f "${container}" >/dev/null 2>&1 || true
  docker volume rm "${volume}" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

start() {
  run_image="$1"
  docker run -d --platform "${platform}" --name "${container}" \
    -p 127.0.0.1::8080 -v "${volume}:/data" "${run_image}" >/dev/null
  port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "8080/tcp") 0).HostPort}}' "${container}")"
  base="http://127.0.0.1:${port}"
  for _ in $(seq 1 60); do
    if curl --fail --silent "${base}/readyz" >/dev/null; then return; fi
    sleep 1
  done
  docker logs "${container}"
  return 1
}

docker pull --platform "${platform}" "${image}"
label_version="$(docker image inspect --platform "${platform}" --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "${image}")"
test "${label_version}" = "${version}"

start "${image}"
curl --fail --silent "${base}/healthz" | grep -q 'ok'
curl --fail --silent "${base}/readyz" | grep -q 'ready'
curl --fail --silent "${base}/history/deep/link" | grep -q '<div id="root"'
curl --fail --silent "${base}/api/update" | grep -Fq "\"running_version\":\"${version}\""
curl --fail --silent -X POST "${base}/api/integrations/jellyfin" | grep -q '"configured":true'
curl --fail --silent "${base}/api/setup" | grep -q '"jellyfin":{"configured":true'
docker exec "${container}" watchweaver backup /data/backups/published-smoke.db >/dev/null
docker exec "${container}" test -s /data/backups/published-smoke.db
docker exec "${container}" test -s /data/backups/published-smoke.db.key

started="$(date +%s)"
docker stop --time 15 "${container}" >/dev/null
elapsed="$(( $(date +%s) - started ))"
test "${elapsed}" -le 15
docker rm "${container}" >/dev/null

start "${image}"
curl --fail --silent "${base}/api/setup" | grep -q '"jellyfin":{"configured":true'

if [[ -n "${previous_image}" ]]; then
  docker rm -f "${container}" >/dev/null
  docker volume rm "${volume}" >/dev/null
  container="${container}-upgrade"
  volume="${volume}-upgrade"
  docker pull --platform "${platform}" "${previous_image}"
  start "${previous_image}"
  curl --fail --silent -X POST "${base}/api/integrations/jellyfin" | grep -q '"configured":true'
  docker stop --time 15 "${container}" >/dev/null
  docker rm "${container}" >/dev/null
  start "${image}"
  curl --fail --silent "${base}/readyz" >/dev/null
  curl --fail --silent "${base}/api/setup" | grep -q '"jellyfin":{"configured":true'
fi

echo "Smoke test passed: image=${image} platform=${platform} version=${version} graceful_shutdown_seconds=${elapsed} upgrade_fixture=${previous_image:-none}"
