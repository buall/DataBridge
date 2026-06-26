#!/usr/bin/env bash

set -Eeuo pipefail

DATABRIDGE_NETWORK="${DATABRIDGE_NETWORK:-databridge-compat}"
DATABRIDGE_BACKUP_DIR="${DATABRIDGE_BACKUP_DIR:-/tmp/databridge-compat-backups}"
DATABRIDGE_CONTAINER="${DATABRIDGE_CONTAINER:-databridge}"
DATABRIDGE_IMAGE="${DATABRIDGE_IMAGE:-databridge-compat:local}"
DATABRIDGE_START_MODE="${DATABRIDGE_START_MODE:-auto}"
DATABRIDGE_REBUILD="${DATABRIDGE_REBUILD:-0}"

case_containers=()
case_volumes=()

require_commands() {
  local missing=()
  for command in "$@"; do
    if ! command -v "$command" >/dev/null 2>&1; then
      missing+=("$command")
    fi
  done
  if (( ${#missing[@]} > 0 )); then
    echo "Missing required commands: ${missing[*]}" >&2
    return 1
  fi
}

ensure_docker_network() {
  docker network inspect "$DATABRIDGE_NETWORK" >/dev/null 2>&1 || docker network create "$DATABRIDGE_NETWORK" >/dev/null
}

ensure_backup_dir() {
  mkdir -p "$DATABRIDGE_BACKUP_DIR"
  chmod 0777 "$DATABRIDGE_BACKUP_DIR"
}

ensure_databridge() {
  export DATABRIDGE_AUTH_USER="${DATABRIDGE_AUTH_USER:-admin}"
  export DATABRIDGE_AUTH_PASSWORD="${DATABRIDGE_AUTH_PASSWORD:-databridge-compat}"
  if [[ "$DATABRIDGE_START_MODE" == "external" ]]; then
    return 0
  fi
  if [[ "$DATABRIDGE_REBUILD" == "1" ]]; then
    docker rm -f "$DATABRIDGE_CONTAINER" >/dev/null 2>&1 || true
    docker image rm "$DATABRIDGE_IMAGE" >/dev/null 2>&1 || true
  fi
  if docker inspect "$DATABRIDGE_CONTAINER" >/dev/null 2>&1; then
    if [[ "$(docker inspect -f '{{.State.Running}}' "$DATABRIDGE_CONTAINER")" == "true" ]]; then
      docker network connect "$DATABRIDGE_NETWORK" "$DATABRIDGE_CONTAINER" >/dev/null 2>&1 || true
      return 0
    fi
    docker start "$DATABRIDGE_CONTAINER" >/dev/null
    docker network connect "$DATABRIDGE_NETWORK" "$DATABRIDGE_CONTAINER" >/dev/null 2>&1 || true
    return 0
  fi
  docker image inspect "$DATABRIDGE_IMAGE" >/dev/null 2>&1 || docker build -t "$DATABRIDGE_IMAGE" .
  local auth_password session_secret
  auth_password="$DATABRIDGE_AUTH_PASSWORD"
  session_secret="${DATABRIDGE_SESSION_SECRET:-$(openssl rand -hex 32)}"
  docker run -d \
    --name "$DATABRIDGE_CONTAINER" \
    --network "$DATABRIDGE_NETWORK" \
    -p 8081:8081 \
    -e DATABRIDGE_ADDR=:8081 \
    -e DATABRIDGE_AUTH_USER="$DATABRIDGE_AUTH_USER" \
    -e DATABRIDGE_AUTH_PASSWORD="$auth_password" \
    -e DATABRIDGE_SESSION_SECRET="$session_secret" \
    -e DATABRIDGE_PG_JOBS=2 \
    -v databridge-compat-data:/data \
    -v "$DATABRIDGE_BACKUP_DIR:$DATABRIDGE_BACKUP_DIR" \
    "$DATABRIDGE_IMAGE" >/dev/null
}

wait_http_ready() {
  local url="$1"
  local timeout="${2:-120}"
  local start now
  start="$(date +%s)"
  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    now="$(date +%s)"
    if (( now - start > timeout )); then
      echo "Timed out waiting for $url" >&2
      return 1
    fi
    sleep 2
  done
}

docker_case_register() {
  case_containers+=("$1")
  case_volumes+=("$2")
}

docker_case_preclean() {
  local name="$1"
  local volume="$2"
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
}

docker_case_cleanup() {
  local name
  for name in "${case_containers[@]:-}"; do
    if [[ -n "$name" ]]; then
      docker rm -f "$name" >/dev/null 2>&1 || true
    fi
  done
  local volume
  for volume in "${case_volumes[@]:-}"; do
    if [[ -n "$volume" ]]; then
      docker volume rm "$volume" >/dev/null 2>&1 || true
    fi
  done
}

collect_container_logs() {
  local report_dir="$1"
  local name
  for name in "${case_containers[@]:-}"; do
    if [[ -n "$name" ]]; then
      docker logs "$name" >"$report_dir/container-$name.log" 2>&1 || true
    fi
  done
  docker logs "$DATABRIDGE_CONTAINER" >"$report_dir/container-$DATABRIDGE_CONTAINER.log" 2>&1 || true
}

matrix_update() {
  local db_type="$1"
  local source_version="$2"
  local target_version="$3"
  local connection="$4"
  local discovery="$5"
  local migration="$6"
  local backup="$7"
  local restore="$8"
  local conclusion="$9"
  local note="${10:-}"
  local matrix="docs/compatibility-matrix.md"
  local tmp
  tmp="$(mktemp)"
  awk -v db="$db_type" -v src="$source_version" -v tgt="$target_version" \
    -v conn="$connection" -v disc="$discovery" -v mig="$migration" \
    -v bak="$backup" -v res="$restore" -v conc="$conclusion" -v note="$note" '
      BEGIN { replaced=0 }
      /^Last updated:/ { print "Last updated: " strftime("%Y-%m-%d %H:%M:%S %Z"); next }
      $0 ~ "^\\| " db " \\| " src " \\| " tgt " \\|" {
        print "| " db " | " src " | " tgt " | " conn " | " disc " | " mig " | " bak " | " res " | " conc " | " note " |";
        replaced=1;
        next;
      }
      { print }
      END {
        if (!replaced) {
          print "| " db " | " src " | " tgt " | " conn " | " disc " | " mig " | " bak " | " res " | " conc " | " note " |";
        }
      }
    ' "$matrix" >"$tmp"
  mv "$tmp" "$matrix"
}

case_slug() {
  local db_type="$1"
  local source_version="$2"
  local target_version="$3"
  echo "${db_type}-${source_version}-to-${target_version}" | tr '.+' '--' | tr -cd '[:alnum:]-'
}
