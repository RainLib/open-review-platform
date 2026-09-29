#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

if [ -n "${DOCKER_BIN:-}" ]; then
  docker_bin=$DOCKER_BIN
elif command -v docker >/dev/null 2>&1; then
  docker_bin=$(command -v docker)
elif [ -x /Applications/Docker.app/Contents/Resources/bin/docker ]; then
  docker_bin=/Applications/Docker.app/Contents/Resources/bin/docker
else
  echo "docker is required to verify the observability configuration" >&2
  exit 1
fi

"$docker_bin" compose --project-directory "$repository_root" --profile observability config --quiet
"$docker_bin" run --rm --entrypoint promtool \
  -v "$repository_root/deploy/observability/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
  -v "$repository_root/deploy/observability/open-review-alerts.yml:/etc/prometheus/open-review-alerts.yml:ro" \
  prom/prometheus:v3.5.1 check config /etc/prometheus/prometheus.yml
"$docker_bin" run --rm --entrypoint promtool \
  -v "$repository_root/deploy/observability/open-review-alerts.yml:/etc/prometheus/open-review-alerts.yml:ro" \
  prom/prometheus:v3.5.1 check rules /etc/prometheus/open-review-alerts.yml
