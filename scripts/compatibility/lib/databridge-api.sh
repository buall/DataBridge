#!/usr/bin/env bash

set -Eeuo pipefail

DATABRIDGE_API_BASE="${DATABRIDGE_API_BASE:-http://127.0.0.1:8081/api}"
DATABRIDGE_COOKIE_JAR="${DATABRIDGE_COOKIE_JAR:-/tmp/databridge-compat-cookie.jar}"
DATABRIDGE_AUTH_USER="${DATABRIDGE_AUTH_USER:-}"
DATABRIDGE_AUTH_PASSWORD="${DATABRIDGE_AUTH_PASSWORD:-}"

api_log_dir="${CASE_REPORT_DIR:-scripts/compatibility/reports}"

api_init() {
  mkdir -p "$api_log_dir"
  rm -f "$DATABRIDGE_COOKIE_JAR"
  api_get "/healthz" "$api_log_dir/api-healthz.json" >/dev/null
  local session
  session="$(api_get "/auth/session" "$api_log_dir/api-session.json")"
  if jq -e '.authenticated == true' <<<"$session" >/dev/null; then
    return 0
  fi
  if jq -e '.configured == true' <<<"$session" >/dev/null; then
    if [[ -z "$DATABRIDGE_AUTH_USER" || -z "$DATABRIDGE_AUTH_PASSWORD" ]]; then
      echo "DataBridge authentication is enabled; set DATABRIDGE_AUTH_USER and DATABRIDGE_AUTH_PASSWORD" >&2
      return 1
    fi
    api_post "/auth/login" "$(jq -nc --arg u "$DATABRIDGE_AUTH_USER" --arg p "$DATABRIDGE_AUTH_PASSWORD" '{username:$u,password:$p}')" "$api_log_dir/api-login.json" >/dev/null
  fi
}

api_get() {
  local path="$1"
  local output="${2:-}"
  local tmp
  tmp="$(mktemp)"
  curl -fsS -b "$DATABRIDGE_COOKIE_JAR" -c "$DATABRIDGE_COOKIE_JAR" "$DATABRIDGE_API_BASE$path" -o "$tmp"
  if [[ -n "$output" ]]; then
    cp "$tmp" "$output"
  fi
  cat "$tmp"
  rm -f "$tmp"
}

api_post() {
  local path="$1"
  local body="$2"
  local output="${3:-}"
  local tmp
  tmp="$(mktemp)"
  curl -fsS -b "$DATABRIDGE_COOKIE_JAR" -c "$DATABRIDGE_COOKIE_JAR" \
    -H "Content-Type: application/json" \
    -X POST \
    -d "$body" \
    "$DATABRIDGE_API_BASE$path" \
    -o "$tmp"
  if [[ -n "$output" ]]; then
    cp "$tmp" "$output"
  fi
  cat "$tmp"
  rm -f "$tmp"
}

api_delete() {
  local path="$1"
  local output="${2:-}"
  local tmp
  tmp="$(mktemp)"
  curl -fsS -b "$DATABRIDGE_COOKIE_JAR" -c "$DATABRIDGE_COOKIE_JAR" \
    -X DELETE \
    "$DATABRIDGE_API_BASE$path" \
    -o "$tmp"
  if [[ -n "$output" ]]; then
    cp "$tmp" "$output"
  fi
  cat "$tmp"
  rm -f "$tmp"
}

db_connection_payload() {
  local type="$1"
  local name="$2"
  local host="$3"
  local port="$4"
  local username="$5"
  local password="$6"
  local ssl_mode="${7:-disable}"
  jq -nc \
    --arg type "$type" \
    --arg name "$name" \
    --arg host "$host" \
    --argjson port "$port" \
    --arg username "$username" \
    --arg password "$password" \
    --arg sslMode "$ssl_mode" \
    '{type:$type,name:$name,host:$host,port:$port,username:$username,password:$password,sslMode:$sslMode}'
}

create_connection() {
  local payload="$1"
  local label="$2"
  api_post "/connections/test" "$payload" "$CASE_REPORT_DIR/api-test-connection-$label.json" >/dev/null
  api_post "/connections" "$payload" "$CASE_REPORT_DIR/api-create-connection-$label.json" | jq -r '.id'
}

delete_connection_quiet() {
  local id="$1"
  [[ -z "$id" ]] && return 0
  api_delete "/connections/$id" "$CASE_REPORT_DIR/api-delete-connection-$id.json" >/dev/null 2>&1 || true
}

discover_databases() {
  local connection_id="$1"
  local label="$2"
  api_get "/connections/$connection_id/databases" "$CASE_REPORT_DIR/api-databases-$label.json"
}

discover_objects() {
  local connection_id="$1"
  local database="$2"
  local label="$3"
  api_get "/connections/$connection_id/databases/$(urlencode "$database")/tables" "$CASE_REPORT_DIR/api-objects-$label.json"
}

create_migration() {
  local name="$1"
  local engine="$2"
  local source_id="$3"
  local source_db="$4"
  local target_id="$5"
  local target_db="$6"
  local tables_json="$7"
  local payload
  payload="$(jq -nc \
    --arg name "$name" \
    --arg engine "$engine" \
    --arg sourceId "$source_id" \
    --arg sourceDatabase "$source_db" \
    --arg targetId "$target_id" \
    --arg targetDatabase "$target_db" \
    --argjson tables "$tables_json" \
    '{name:$name,engine:$engine,sourceId:$sourceId,sourceDatabase:$sourceDatabase,targetId:$targetId,targetDatabase:$targetDatabase,tables:$tables,conflictStrategy:"overwrite"}')"
  api_post "/migrations" "$payload" "$CASE_REPORT_DIR/api-create-migration.json" | jq -r '.id'
}

poll_migration() {
  local id="$1"
  local timeout="${2:-900}"
  poll_job "/migrations/$id" "$CASE_REPORT_DIR/api-migration-$id.json" "$timeout"
}

create_backup() {
  local name="$1"
  local source_id="$2"
  local source_db="$3"
  local destination="$4"
  local payload
  payload="$(jq -nc \
    --arg name "$name" \
    --arg sourceId "$source_id" \
    --arg sourceDatabase "$source_db" \
    --arg destination "$destination" \
    '{name:$name,sourceId:$sourceId,sourceDatabase:$sourceDatabase,destinationType:"local",destination:$destination,scheduleMode:"manual"}')"
  api_post "/backups" "$payload" "$CASE_REPORT_DIR/api-create-backup.json" | jq -r '.id'
}

poll_backup() {
  local id="$1"
  local label="${2:-backup}"
  local timeout="${3:-900}"
  poll_job "/backups/$id" "$CASE_REPORT_DIR/api-$label-$id.json" "$timeout"
}

restore_backup() {
  local backup_id="$1"
  local target_id="$2"
  local target_db="$3"
  local payload
  payload="$(jq -nc \
    --arg targetId "$target_id" \
    --arg targetDatabase "$target_db" \
    '{targetId:$targetId,targetDatabase:$targetDatabase,strategy:"overwrite"}')"
  api_post "/backups/$backup_id/restore" "$payload" "$CASE_REPORT_DIR/api-create-restore.json" | jq -r '.id'
}

poll_job() {
  local path="$1"
  local output="$2"
  local timeout="$3"
  local start
  start="$(date +%s)"
  while true; do
    local body status now
    body="$(api_get "$path" "$output")"
    status="$(jq -r '.status // empty' <<<"$body")"
    case "$status" in
      completed)
        return 0
        ;;
      failed|cancelled)
        jq -r '.error // "job failed without error detail"' <<<"$body" >&2
        return 1
        ;;
    esac
    now="$(date +%s)"
    if (( now - start > timeout )); then
      echo "Timed out polling $path after ${timeout}s" >&2
      return 1
    fi
    sleep 3
  done
}

extract_events() {
  local json_file="$1"
  local output="$2"
  if [[ -s "$json_file" ]]; then
    jq -r '.events[]? | [.time, .level, .message] | @tsv' "$json_file" >"$output" || true
  fi
}

urlencode() {
  jq -nr --arg v "$1" '$v|@uri'
}
