#!/usr/bin/env bash
set -euo pipefail

# Choose a local Compose footprint explicitly. Existing workers stay active by
# default; --stop-unused pauses optional project workers after checking whether
# durable review, Issue, or Agent Work would be interrupted.
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_dir}"

usage() {
    printf 'Usage: %s ui|auth|setup|review|compact [--github-app] [--build] [--stop-unused] [--allow-paused-work]\n' "$0" >&2
    printf '  ui      PostgreSQL + API + hot-reload Console (no review workers)\n' >&2
    printf '  auth    PostgreSQL + API + production-shaped Console for OAuth checks\n' >&2
    printf '  setup   Auth mode plus one provider verification worker; no broker or review consumers\n' >&2
    printf '  review  PR/Issue review workers via the lean Compose overlay\n' >&2
    printf '  compact Development-only PR/Issue review with one supervised worker container\n' >&2
    printf '  --stop-unused  With ui/auth/setup/compact, pause other project services after startup\n' >&2
    printf '  --allow-paused-work  Explicitly pause nonterminal review, Issue or Agent work too\n' >&2
    exit 2
}

[[ $# -ge 1 ]] || usage
mode="$1"
shift
github_app=false
build=false
stop_unused=false
allow_paused_work=false
for option in "$@"; do
    case "${option}" in
        --github-app) github_app=true ;;
        --build) build=true ;;
        --stop-unused) stop_unused=true ;;
        --allow-paused-work) allow_paused_work=true ;;
        *) usage ;;
    esac
done
if [[ "${allow_paused_work}" == true && "${stop_unused}" != true ]]; then
    printf '%s\n' '--allow-paused-work requires --stop-unused.' >&2
    exit 2
fi

compose=(docker compose -f docker-compose.yml)
services=()
case "${mode}" in
    ui|auth|setup)
        if [[ "${github_app}" == true ]]; then
            if [[ "${mode}" != setup ]]; then
                printf '%s\n' '--github-app is only needed for setup, review or compact workers.' >&2
                exit 2
            fi
            compose+=(-f docker-compose.github-app.yml)
        fi
        if [[ "${mode}" == ui ]]; then
            compose+=(-f docker-compose.dev.yml)
        elif [[ "${mode}" == setup ]]; then
            compose+=(-f docker-compose.setup.yml)
        fi
        services=(postgres migrate control-api console)
        if [[ "${mode}" == setup ]]; then
            services+=(provider-prober)
        fi
        ;;
    review)
        if [[ "${stop_unused}" == true ]]; then
            printf '%s\n' '--stop-unused is only supported for ui/auth/setup/compact; review needs its queue consumers.' >&2
            exit 2
        fi
        if [[ "${github_app}" == true ]]; then
            compose+=(-f docker-compose.github-app.yml)
        fi
        compose+=(-f docker-compose.core.yml)
        ;;
    compact)
        if [[ "${ENVIRONMENT:-development}" != development ]]; then
            printf '%s\n' 'compact mode is restricted to ENVIRONMENT=development.' >&2
            exit 2
        fi
        if [[ "${github_app}" == true ]]; then
            compose+=(-f docker-compose.github-app.yml)
        fi
        compose+=(-f docker-compose.core.yml -f docker-compose.compact-review.yml)
        # Compose loads .env separately from this shell. Check the effective
        # value without printing the other rendered environment or secrets.
        compact_environment="$("${compose[@]}" config --format json | node -e 'let data="";process.stdin.on("data",chunk=>data+=chunk).on("end",()=>console.log(JSON.parse(data).services["compact-review"].environment.ENVIRONMENT))')"
        if [[ "${compact_environment}" != development ]]; then
            printf '%s\n' 'compact mode requires the rendered Compose ENVIRONMENT=development.' >&2
            exit 2
        fi
        services=(postgres migrate rabbitmq control-api console compact-review)
        ;;
    *) usage ;;
esac

if [[ "${stop_unused}" == true && "${allow_paused_work}" != true && "${mode}" != compact ]]; then
    # A paused consumer retains durable messages, but operators should not
    # mistake that for a completed review. An installation that is inactive or
    # unverified cannot be claimed by either worker; do not let those stranded
    # records force a full stack for an unrelated UI/OAuth session.
    postgres_container="$(docker compose -f docker-compose.yml ps --status running -q postgres)"
    if [[ -z "${postgres_container}" ]]; then
        running_project_services="$(docker compose -f docker-compose.yml ps --status running --services)"
        if [[ -n "${running_project_services}" ]]; then
            printf '%s\n' 'Cannot verify pending work while PostgreSQL is stopped; no running project worker was paused.' >&2
            exit 1
        fi
    else
        if ! pending_work="$(docker exec "${postgres_container}" psql -U openreview -d openreview -Atq -c "
            SELECT
              (SELECT count(*) FROM review_runs r
               JOIN review_requests request ON request.id = r.request_id
               JOIN provider_installations installation ON installation.id = request.installation_id
               WHERE r.state NOT IN ('completed', 'failed', 'cancelled', 'superseded', 'needs_attention')
                 AND installation.active
                 AND installation.verification_state IN ('legacy', 'verified')) +
              (SELECT count(*) FROM provider_issue_analysis_jobs job
               JOIN provider_installations installation ON installation.id = job.installation_id
               WHERE job.state NOT IN ('completed', 'failed', 'cancelled', 'superseded')
                 AND installation.active
                 AND installation.verification_state IN ('legacy', 'verified')) +
              (SELECT count(*) FROM agent_tasks
               WHERE state IN ('received', 'execution_queued', 'executing'));
        ")"; then
            printf '%s\n' 'Could not verify pending work; no running project worker was paused.' >&2
            exit 1
        fi
        if [[ ! "${pending_work}" =~ ^[0-9]+$ ]]; then
            printf '%s\n' 'Could not verify the pending-work count; workers were not paused.' >&2
            exit 1
        fi
        if [[ "${mode}" == ui || "${mode}" == auth ]]; then
            # Setup mode keeps provider-prober running; ui/auth pause it. A
            # newly authorized connection must not be stranded mid-probe.
            if ! pending_probes="$(docker exec "${postgres_container}" psql -U openreview -d openreview -Atq -c "
                SELECT count(*) FROM provider_health_probes probe
                JOIN provider_installations installation ON installation.id = probe.installation_id
                WHERE probe.state IN ('queued', 'running') AND installation.active;
            ")"; then
                printf '%s\n' 'Could not verify pending provider probes; the verification worker was not paused.' >&2
                exit 1
            fi
            if [[ ! "${pending_probes}" =~ ^[0-9]+$ ]]; then
                printf '%s\n' 'Could not verify the pending provider-probe count; the verification worker was not paused.' >&2
                exit 1
            fi
            pending_work="$((pending_work + pending_probes))"
        fi
        if [[ "${pending_work}" =~ ^[0-9]+$ ]] && (( pending_work > 0 )); then
            printf 'Refusing to pause workers: %s runnable review/Issue/Agent/provider verification job(s) remain.\n' "${pending_work}" >&2
            printf '%s\n' 'Wait for completion, or repeat with --allow-paused-work if pausing them is intentional.' >&2
            exit 1
        fi
    fi
fi

up=(up -d)
if [[ "${mode}" == compact ]]; then
    # RabbitMQ's configured 2 GB disk watermark needs write headroom for its
    # persisted quorum logs. Check Docker's filesystem before a new image
    # build, stopping split workers or starting the broker; never prune
    # volumes implicitly.
    docker compose -f docker-compose.yml up -d --no-build --pull never --no-recreate postgres
    postgres_container="$(docker compose -f docker-compose.yml ps --status running -q postgres)"
    free_kib="$(docker exec "${postgres_container}" df -Pk / | awk 'NR == 2 { print $4 }')"
    if [[ ! "${free_kib}" =~ ^[0-9]+$ ]] || (( free_kib < 3145728 )); then
        printf 'Compact review requires at least 3 GiB free inside Docker before RabbitMQ starts (observed %s KiB). No worker was stopped.\n' "${free_kib:-unknown}" >&2
        exit 1
    fi
    if [[ "${stop_unused}" == true && "${allow_paused_work}" != true ]]; then
        # Compact continues PR/Issue jobs, but optional Agent Work containers
        # would be paused. Refuse to interrupt their active task lifecycle.
        agent_pending="$(docker exec "${postgres_container}" psql -U openreview -d openreview -Atq -c "
            SELECT count(*) FROM agent_tasks
            WHERE state IN ('received', 'execution_queued', 'executing');
        ")"
        if [[ ! "${agent_pending}" =~ ^[0-9]+$ ]]; then
            printf '%s\n' 'Could not verify active Agent Work; optional workers were not paused.' >&2
            exit 1
        fi
        if (( agent_pending > 0 )); then
            printf 'Refusing to pause optional workers: %s active Agent Work task(s) remain.\n' "${agent_pending}" >&2
            printf '%s\n' 'Wait for completion, or repeat with --allow-paused-work if pausing them is intentional.' >&2
            exit 1
        fi
    fi
    if [[ "${build}" == true ]]; then
        # Build only the local bundle. Rebuilding API/Console on every mode
        # switch is slow and can replace an already validated targeted image.
        "${compose[@]}" build compact-review
        free_kib="$(docker exec "${postgres_container}" df -Pk / | awk 'NR == 2 { print $4 }')"
        if [[ ! "${free_kib}" =~ ^[0-9]+$ ]] || (( free_kib < 3145728 )); then
            printf 'Compact review build left less than 3 GiB free inside Docker (observed %s KiB). No worker was stopped.\n' "${free_kib:-unknown}" >&2
            exit 1
        fi
    else
        compact_image="${OPEN_REVIEW_COMPACT_REVIEW_IMAGE:-open-review-platform-compact-review:local}"
        if ! docker image inspect "${compact_image}" >/dev/null 2>&1; then
            printf 'Missing %s. Run compact --build once.\n' "${compact_image}" >&2
            exit 1
        fi
    fi
    # Two consumers with the same worker identity must not race provider
    # publication during a mode switch. The bundle is already built/available
    # before pausing these exact split equivalents; optional Agent workers are
    # never stopped by this local review shortcut.
    split_services=(outbox-relay acknowledger interaction-responder interaction-admitter terminal-reporter issue-publisher issue-triager model-prober provider-prober provider-feedback-poller runner review-scheduler rule-exception-expirer rule-rollout-monitor sso-prober data-governance-worker)
    docker compose -f docker-compose.yml stop "${split_services[@]}"
    up+=(--no-build --pull never --no-recreate)
    if [[ "${build}" == true ]]; then
        services=(postgres migrate rabbitmq control-api console)
    fi
elif [[ "${mode}" == setup ]]; then
    # Initial provider probes use PostgreSQL directly. Building only this
    # worker keeps an onboarding session from rebuilding API and Console.
    provider_prober_image="${OPEN_REVIEW_PROVIDER_PROBER_IMAGE:-open-review-platform-provider-prober:local}"
    if [[ "${build}" == true ]]; then
        "${compose[@]}" build provider-prober
    elif ! docker image inspect "${provider_prober_image}" >/dev/null 2>&1; then
        printf 'Missing %s. Run setup --build once.\n' "${provider_prober_image}" >&2
        exit 1
    fi
    compact_container="$(docker compose -f docker-compose.yml -f docker-compose.core.yml -f docker-compose.compact-review.yml ps --status running -q compact-review)"
    if [[ -n "${compact_container}" ]]; then
        if [[ "${stop_unused}" != true ]]; then
            printf '%s\n' 'Compact review already includes provider verification. Use compact mode, or switch with setup --stop-unused after checking active work.' >&2
            exit 1
        fi
        # The pending-work guard above ran before this stop. Never run two
        # provider probers with the same worker identity during a mode switch.
        docker compose -f docker-compose.yml -f docker-compose.core.yml -f docker-compose.compact-review.yml stop compact-review
    fi
    up+=(--no-build --pull never --no-recreate)
    if [[ "${build}" == true ]]; then
        # The explicit force-recreate below starts the new binary exactly
        # once, after database migrations and the existing API/Console.
        services=(postgres migrate control-api console)
    fi
elif [[ "${build}" == true ]]; then
    if [[ "${mode}" == review ]]; then
        # A complete shared-core build must not quietly keep a separately
        # pinned, older API. Compact/setup builds are intentionally scoped and
        # may retain the API pin while replacing only their own worker image.
        image_pair="$("${compose[@]}" config --format json | node -e 'let data="";process.stdin.on("data",chunk=>data+=chunk).on("end",()=>{const services=JSON.parse(data).services;console.log(services["control-api"].image+"\t"+services.migrate.image)})')"
        read -r configured_api_image shared_core_image <<< "${image_pair}"
        if [[ "${configured_api_image}" != "${shared_core_image}" ]]; then
            printf 'Full review --build requires one compatible shared-core release. Unset OPEN_REVIEW_CONTROL_API_IMAGE (currently %s) before rebuilding all core services.\n' "${configured_api_image}" >&2
            exit 1
        fi
        # Do not pause a running compact bundle until replacement images have
        # built successfully.
        "${compose[@]}" build
        up+=(--no-build --pull never)
    else
        up+=(--build)
    fi
else
    # Mode switches must not start an implicit BuildKit job or pull an image
    # while the broker is close to its free-disk alarm. Build explicitly.
    up+=(--no-build --pull never)
    if [[ "${mode}" != ui ]]; then
        # Preserve validated images from prior targeted rollouts. Some workers
        # and the Console may have been launched with a temporary Compose
        # overlay, so a mode switch must start/stop them without silently
        # replacing them with an older default image. --build is explicit.
        up+=(--no-recreate)
    fi
fi
if [[ "${mode}" == review ]]; then
    # The inverse switch must also have only one consumer per durable queue.
    docker compose -f docker-compose.yml -f docker-compose.core.yml -f docker-compose.compact-review.yml stop compact-review
fi
"${compose[@]}" "${up[@]}" "${services[@]}"
if [[ "${mode}" == compact && "${build}" == true ]]; then
    # The final --no-deps recreate deliberately preserves the validated API,
    # Console, and broker containers. It also bypasses Compose's dependency
    # health wait, so do not launch broker consumers until RabbitMQ is ready.
    rabbitmq_container="$("${compose[@]}" ps -q rabbitmq)"
    if [[ -z "${rabbitmq_container}" ]]; then
        printf '%s\n' 'RabbitMQ was not created; compact review was not started.' >&2
        exit 1
    fi
    rabbitmq_ready=false
    for ((attempt=0; attempt<60; attempt++)); do
        rabbitmq_health="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "${rabbitmq_container}" 2>/dev/null || true)"
        if [[ "${rabbitmq_health}" == healthy ]]; then
            rabbitmq_ready=true
            break
        fi
        sleep 2
    done
    if [[ "${rabbitmq_ready}" != true ]]; then
        printf 'RabbitMQ did not become healthy; compact review was not started (last state: %s).\n' "${rabbitmq_health:-unknown}" >&2
        exit 1
    fi
    "${compose[@]}" up -d --no-deps --force-recreate --no-build --pull never compact-review
fi
if [[ "${mode}" == setup && "${build}" == true ]]; then
    "${compose[@]}" up -d --no-deps --force-recreate --no-build --pull never provider-prober
fi

if [[ "${stop_unused}" == true ]]; then
    # Only this Compose project's services are considered. Stopping a consumer
    # preserves its durable queue messages, but new reviews cannot progress
    # until review mode is started again.
    if [[ "${mode}" != compact ]]; then
        # The bundle is an orphan from the base ui/auth/setup Compose view. Stop it
        # with the override that declares it, before stopping RabbitMQ.
        docker compose -f docker-compose.yml -f docker-compose.core.yml -f docker-compose.compact-review.yml stop compact-review
    fi
    running_services="$("${compose[@]}" ps --status running --services)"
    unused=()
    while IFS= read -r service; do
        if [[ "${mode}" == compact ]]; then
            case "${service}" in
                ''|postgres|rabbitmq|control-api|console|compact-review) ;;
                *) unused+=("${service}") ;;
            esac
        elif [[ "${mode}" == setup ]]; then
            case "${service}" in
                ''|postgres|control-api|console|provider-prober) ;;
                *) unused+=("${service}") ;;
            esac
        else
            case "${service}" in
                ''|postgres|control-api|console) ;;
                *) unused+=("${service}") ;;
            esac
        fi
    done <<< "${running_services}"
    if [[ "${unused[*]-}" != '' ]]; then
        printf 'Pausing unused project services: %s\n' "${unused[*]}"
        "${compose[@]}" stop "${unused[@]}"
    fi
fi

if [[ "${mode}" == ui || "${mode}" == auth ]]; then
    printf '%s\n' "${mode} mode does not run webhooks, reviews, Issues, or merge-gate workers."
    if [[ "${mode}" == auth ]]; then
        printf '%s\n' 'Auth mode serves the production-shaped Console; rebuild only when its source or image inputs change.'
    fi
    if [[ "${stop_unused}" == true ]]; then
        printf '%s\n' 'Only PostgreSQL, API, and Console remain active; queued reviews resume in review or compact mode.'
    else
        printf '%s\n' 'Existing workers are not stopped automatically; use --stop-unused only when pausing live review processing is acceptable.'
    fi
elif [[ "${mode}" == setup ]]; then
    printf '%s\n' 'Setup mode runs read-only provider verification and repository inventory without RabbitMQ or PR/Issue review workers.'
    printf '%s\n' 'PostgreSQL, API, Console, and provider-prober are the four required long-running services.'
    if [[ "${stop_unused}" == true ]]; then
        printf '%s\n' 'Other project workers were paused; review and Agent Work processing resume in their respective modes.'
    else
        printf '%s\n' 'Existing review workers are not stopped automatically; use --stop-unused only after checking active work.'
    fi
elif [[ "${mode}" == review ]]; then
    printf '%s\n' 'Review mode excludes optional Agent Work, notifications, feedback, SSO, and governance workers.'
    printf '%s\n' 'Existing optional containers are not stopped automatically.'
else
    printf '%s\n' 'Compact mode runs PR/Issue review, model probes, one-shot scheduling, feedback, exception expiry and Canary monitoring through one development-only supervised bundle.'
    printf '%s\n' 'PostgreSQL, RabbitMQ, API, Console, and compact-review are the five required long-running services.'
    if [[ "${stop_unused}" == true ]]; then
        printf '%s\n' 'Optional project workers were paused; Agent Work resumes with its own services. Notifications run here only when explicitly enabled.'
    else
        printf '%s\n' 'Agent Work, SSO, and other optional workers remain separate; notifications require explicit compact opt-in. Use --stop-unused after the active-task safety check.'
    fi
fi
