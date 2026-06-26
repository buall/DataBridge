#!/usr/bin/env bash

set -Eeuo pipefail

run_mongo_case() {
  CASE_SOURCE_VERSION="$1"
  CASE_TARGET_VERSION="$2"
  CASE_SOURCE_CONN=""
  CASE_TARGET_CONN=""
  CASE_CONNECTION_STATUS="failed"
  CASE_DISCOVERY_STATUS="failed"
  CASE_MIGRATION_STATUS="failed"
  CASE_BACKUP_STATUS="failed"
  CASE_RESTORE_STATUS="failed"
  CASE_CONCLUSION="failed"
  CASE_NOTE=""
  local source_version="$CASE_SOURCE_VERSION"
  local target_version="$CASE_TARGET_VERSION"
  local slug source_name target_name source_volume target_volume
  slug="$(case_slug mongo "$source_version" "$target_version")"
  CASE_REPORT_DIR="scripts/compatibility/reports/$slug-$(date +%Y%m%d-%H%M%S)"
  export CASE_REPORT_DIR
  mkdir -p "$CASE_REPORT_DIR"
  exec > >(tee -a "$CASE_REPORT_DIR/runner.log") 2>&1

  source_name="compat-${slug}-source"
  target_name="compat-${slug}-target"
  source_volume="${source_name}-data"
  target_volume="${target_name}-data"
  docker_case_register "$source_name" "$source_volume"
  docker_case_register "$target_name" "$target_volume"

  cleanup() {
    local exit_code=$?
    collect_container_logs "$CASE_REPORT_DIR"
    delete_connection_quiet "${CASE_SOURCE_CONN:-}"
    delete_connection_quiet "${CASE_TARGET_CONN:-}"
    docker_case_cleanup
    if [[ "${CASE_CONCLUSION:-failed}" == "failed" && -z "${CASE_NOTE:-}" ]]; then
      CASE_NOTE="runner exited with code $exit_code"
    fi
    matrix_update "MongoDB" "$CASE_SOURCE_VERSION" "$CASE_TARGET_VERSION" "$CASE_CONNECTION_STATUS" "$CASE_DISCOVERY_STATUS" "$CASE_MIGRATION_STATUS" "$CASE_BACKUP_STATUS" "$CASE_RESTORE_STATUS" "$CASE_CONCLUSION" "$CASE_NOTE"
    return "$exit_code"
  }
  trap cleanup EXIT

  echo "Running MongoDB compatibility case $source_version -> $target_version"
  docker_case_preclean "$source_name" "$source_volume"
  docker_case_preclean "$target_name" "$target_volume"

  docker run -d --name "$source_name" --network "$DATABRIDGE_NETWORK" -v "$source_volume:/data/db" \
    -e MONGO_INITDB_ROOT_USERNAME=compat -e MONGO_INITDB_ROOT_PASSWORD=compatpass \
    "mongo:$source_version" >/dev/null
  docker run -d --name "$target_name" --network "$DATABRIDGE_NETWORK" -v "$target_volume:/data/db" \
    -e MONGO_INITDB_ROOT_USERNAME=compat -e MONGO_INITDB_ROOT_PASSWORD=compatpass \
    "mongo:$target_version" >/dev/null

  wait_mongo_ready "$source_name"
  wait_mongo_ready "$target_name"
  docker cp scripts/compatibility/seed/mongo.js "$source_name:/tmp/mongo.js"
  docker exec "$source_name" mongosh -u compat -p compatpass --authenticationDatabase admin /tmp/mongo.js

  api_init
  local source_payload target_payload
  source_payload="$(db_connection_payload MongoDB "compat mongo $source_version source" "$source_name" 27017 compat compatpass disable)"
  target_payload="$(db_connection_payload MongoDB "compat mongo $target_version target" "$target_name" 27017 compat compatpass disable)"
  CASE_SOURCE_CONN="$(create_connection "$source_payload" source)"
  CASE_TARGET_CONN="$(create_connection "$target_payload" target)"
  CASE_CONNECTION_STATUS="ok"

  discover_databases "$CASE_SOURCE_CONN" source >/dev/null
  local objects tables_json
  objects="$(discover_objects "$CASE_SOURCE_CONN" compat_source source)"
  tables_json="$(jq -c '[.[].name] | map(select(. == "compat_users" or . == "compat_orders"))' <<<"$objects")"
  if [[ "$(jq 'length' <<<"$tables_json")" -lt 2 ]]; then
    CASE_NOTE="MongoDB collection discovery did not return expected compat collections"
    return 1
  fi
  CASE_DISCOVERY_STATUS="ok"

  local migration_id
  migration_id="$(create_migration "compat mongo $source_version to $target_version" native "$CASE_SOURCE_CONN" compat_source "$CASE_TARGET_CONN" compat_target "$tables_json")"
  poll_migration "$migration_id" 900
  extract_events "$CASE_REPORT_DIR/api-migration-$migration_id.json" "$CASE_REPORT_DIR/migration-events.tsv"
  verify_mongo_counts "$target_name" compat_target
  CASE_MIGRATION_STATUS="ok"

  local backup_id restore_id
  backup_id="$(create_backup "compat mongo backup $source_version" "$CASE_SOURCE_CONN" compat_source "$DATABRIDGE_BACKUP_DIR")"
  poll_backup "$backup_id" backup 900
  extract_events "$CASE_REPORT_DIR/api-backup-$backup_id.json" "$CASE_REPORT_DIR/backup-events.tsv"
  CASE_BACKUP_STATUS="ok"

  restore_id="$(restore_backup "$backup_id" "$CASE_TARGET_CONN" compat_restore)"
  poll_backup "$restore_id" restore 900
  extract_events "$CASE_REPORT_DIR/api-restore-$restore_id.json" "$CASE_REPORT_DIR/restore-events.tsv"
  verify_mongo_counts "$target_name" compat_restore
  CASE_RESTORE_STATUS="ok"

  CASE_CONCLUSION="passed"
  CASE_NOTE="report: $CASE_REPORT_DIR"
}

wait_mongo_ready() {
  local container="$1"
  local timeout=180
  local start now
  start="$(date +%s)"
  until docker exec "$container" mongosh -u compat -p compatpass --authenticationDatabase admin --quiet --eval 'db.adminCommand({ ping: 1 }).ok' 2>/dev/null | grep -q 1; do
    now="$(date +%s)"
    if (( now - start > timeout )); then
      echo "Timed out waiting for MongoDB container $container" >&2
      return 1
    fi
    sleep 2
  done
}

verify_mongo_counts() {
  local container="$1"
  local database="$2"
  local users orders
  users="$(docker exec "$container" mongosh -u compat -p compatpass --authenticationDatabase admin --quiet --eval "db.getSiblingDB('$database').compat_users.countDocuments()")"
  orders="$(docker exec "$container" mongosh -u compat -p compatpass --authenticationDatabase admin --quiet --eval "db.getSiblingDB('$database').compat_orders.countDocuments()")"
  [[ "$users" == "1000" && "$orders" == "1000" ]]
}
