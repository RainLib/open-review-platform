#!/usr/bin/env bash
set -euo pipefail

# Build the exact Linux architecture used by Docker Desktop for the disposable
# GitLab CE overlay. Building these binaries on macOS without GOARCH produces
# a Darwin or host-architecture executable; bind-mounting that into the runner
# image can silently invoke emulation and makes acceptance results unreliable.
root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output_dir="${GITLAB_E2E_BIN_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/open-review-gitlab-e2e.XXXXXX")}"
go_image="${OPEN_REVIEW_E2E_GO_IMAGE:-golang:1.25.14-alpine}"

detect_arch() {
    local server_arch
    server_arch="$(docker version --format '{{.Server.Arch}}')"
    case "${server_arch}" in
        amd64|x86_64) printf 'amd64' ;;
        arm64|aarch64) printf 'arm64' ;;
        *)
            printf 'Unsupported Docker server architecture %q. Set GITLAB_E2E_GOARCH explicitly.\n' "${server_arch}" >&2
            return 1
            ;;
    esac
}

goarch="${GITLAB_E2E_GOARCH:-$(detect_arch)}"
case "${goarch}" in
    amd64|arm64) ;;
    *)
        printf 'Unsupported GITLAB_E2E_GOARCH %q; expected amd64 or arm64.\n' "${goarch}" >&2
        exit 1
        ;;
esac

module_cache="$(go env GOMODCACHE)"
if [[ ! -d "${module_cache}" ]]; then
    printf 'Go module cache does not exist: %s\n' "${module_cache}" >&2
    exit 1
fi

mkdir -p "${output_dir}"

# Do not use the host `go` command here. A mismatched host compiler can produce
# a runnable binary that differs from the service image (and has already made
# a Docker Desktop acceptance runner execute through the wrong runtime).
docker run --rm --platform "linux/${goarch}" \
    --volume "${root_dir}:/src:ro" \
    --volume "${output_dir}:/out" \
    --volume "${module_cache}:/go/pkg/mod:ro" \
    --workdir /src \
    --env GOARCH="${goarch}" \
    --env GOPROXY=off \
    --env GOSUMDB=off \
    "${go_image}" \
    sh -ec '
        for command in control-api provider-prober interaction-admitter interaction-responder runner terminal-reporter issue-publisher issue-triager provider-feedback-poller agent-task-source-admitter agent-task-runner agent-task-adapter; do
            GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "/out/$command" "./cmd/$command"
        done
    '

printf 'GitLab E2E binaries: %s (linux/%s)\n' "${output_dir}" "${goarch}"
go version -m "${output_dir}/runner" | sed -n '1,8p'
printf 'Run with: GITLAB_E2E_BIN_DIR=%q docker compose -f docker-compose.yml -f deploy/docker-compose.gitlab-e2e.yml up -d --force-recreate\n' "${output_dir}"
