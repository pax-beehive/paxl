#!/bin/sh
set -eu

paxl_repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
team_memory_repo=${TEAM_MEMORY_REPO:-"$(dirname "$(dirname "$paxl_repo_dir")")/team-memory"}
base_compose="$team_memory_repo/tests/onprem-e2e/compose.yaml"
override_compose="$paxl_repo_dir/tests/onprem-e2e/compose.override.yaml"
project_name=${PAXL_ONPREM_E2E_PROJECT:-"paxl-onprem-e2e-$(date -u +%Y%m%d%H%M%S)-$$"}
volume_name="${project_name}_postgres-data"
network_name="${project_name}_default"
temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/paxl-onprem-e2e.XXXXXX")
e2e_binary="$temp_dir/paxl-onprem-channel-e2e"
paxl_binary="$temp_dir/paxl"
runner_image="${project_name}-e2e:latest"

if [ ! -f "$base_compose" ]; then
  echo "Team Memory on-prem E2E compose file not found: $base_compose" >&2
  exit 1
fi

run_compose() {
  PAXL_E2E_BINARY="$e2e_binary" PAXL_E2E_PAXL_BINARY="$paxl_binary" \
    PAXL_E2E_RUNNER_IMAGE="$runner_image" \
    docker compose -p "$project_name" \
    -f "$base_compose" -f "$override_compose" "$@"
}

project_exists() {
  if docker volume inspect "$volume_name" >/dev/null 2>&1; then
    return 0
  fi
  if docker network inspect "$network_name" >/dev/null 2>&1; then
    return 0
  fi
  [ -n "$(run_compose ps -aq)" ]
}

cleanup() {
  exit_status=$1
  trap - EXIT INT TERM
  if [ "$exit_status" -ne 0 ]; then
    run_compose logs --no-color team-memory mock-oidc postgres paxl-e2e >&2 || true
  fi
  if ! run_compose down -v --remove-orphans >/dev/null 2>&1; then
    echo "failed to remove on-prem E2E containers and volumes" >&2
    exit_status=1
  fi
  if docker volume inspect "$volume_name" >/dev/null 2>&1; then
    echo "temporary PostgreSQL volume remains: $volume_name" >&2
    exit_status=1
  fi
  rm -f "$e2e_binary" "$paxl_binary"
  rmdir "$temp_dir" >/dev/null 2>&1 || true
  exit "$exit_status"
}

go_arch=$(go env GOARCH)
GOCACHE=${GOCACHE:-/tmp/paxl-go-cache} CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go test -c \
  -o "$e2e_binary" "$paxl_repo_dir/tests/onprem-e2e"
GOCACHE=${GOCACHE:-/tmp/paxl-go-cache} CGO_ENABLED=0 GOOS=linux GOARCH="$go_arch" go build \
  -o "$paxl_binary" "$paxl_repo_dir/cmd/paxl"
run_compose config --quiet
if project_exists; then
  echo "refusing to reuse existing Docker Compose project: $project_name" >&2
  exit 1
fi

trap 'cleanup $?' EXIT
trap 'exit 130' INT TERM

if [ -n "${PAXL_ONPREM_E2E_CACHED_PROJECT:-}" ]; then
  runner_image="${PAXL_ONPREM_E2E_CACHED_PROJECT}-e2e:latest"
  for service in team-memory mock-extractor mock-oidc; do
    docker tag "${PAXL_ONPREM_E2E_CACHED_PROJECT}-${service}:latest" \
      "${project_name}-${service}:latest"
  done
else
  run_compose build team-memory mock-extractor mock-oidc e2e
fi
run_compose up -d postgres mock-extractor mock-oidc team-memory

if ! docker volume inspect "$volume_name" >/dev/null 2>&1; then
  echo "temporary PostgreSQL volume was not created: $volume_name" >&2
  exit 1
fi

run_compose run --rm paxl-e2e
echo "paxl on-prem channel E2E passed; temporary volume will be removed: $volume_name"
