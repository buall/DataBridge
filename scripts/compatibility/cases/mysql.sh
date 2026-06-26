#!/usr/bin/env bash

set -Eeuo pipefail

run_mysql_case() {
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
  slug="$(case_slug mysql "$source_version" "$target_version")"
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
    matrix_update "MySQL" "$CASE_SOURCE_VERSION" "$CASE_TARGET_VERSION" "$CASE_CONNECTION_STATUS" "$CASE_DISCOVERY_STATUS" "$CASE_MIGRATION_STATUS" "$CASE_BACKUP_STATUS" "$CASE_RESTORE_STATUS" "$CASE_CONCLUSION" "$CASE_NOTE"
    return "$exit_code"
  }
  trap cleanup EXIT

  echo "Running MySQL compatibility case $source_version -> $target_version"
  docker_case_preclean "$source_name" "$source_volume"
  docker_case_preclean "$target_name" "$target_volume"

  docker run -d --name "$source_name" --network "$DATABRIDGE_NETWORK" -v "$source_volume:/var/lib/mysql" \
    -e MYSQL_ROOT_PASSWORD=compatpass -e MYSQL_DATABASE=compat_source -e MYSQL_USER=compat -e MYSQL_PASSWORD=compatpass \
    "mysql:$source_version" --character-set-server=utf8mb4 --collation-server=utf8mb4_unicode_ci >/dev/null
  docker run -d --name "$target_name" --network "$DATABRIDGE_NETWORK" -v "$target_volume:/var/lib/mysql" \
    -e MYSQL_ROOT_PASSWORD=compatpass -e MYSQL_DATABASE=compat_target -e MYSQL_USER=compat -e MYSQL_PASSWORD=compatpass \
    "mysql:$target_version" --character-set-server=utf8mb4 --collation-server=utf8mb4_unicode_ci >/dev/null

  wait_mysql_ready "$source_name"
  wait_mysql_ready "$target_name"
  docker exec -i "$source_name" mysql -ucompat -pcompatpass compat_source < scripts/compatibility/seed/mysql.sql

  api_init
  local source_payload target_payload
  source_payload="$(db_connection_payload MySQL "compat mysql $source_version source" "$source_name" 3306 compat compatpass disable)"
  target_payload="$(db_connection_payload MySQL "compat mysql $target_version target" "$target_name" 3306 compat compatpass disable)"
  CASE_SOURCE_CONN="$(create_connection "$source_payload" source)"
  CASE_TARGET_CONN="$(create_connection "$target_payload" target)"
  CASE_CONNECTION_STATUS="ok"

  discover_databases "$CASE_SOURCE_CONN" source >/dev/null
  discover_databases "$CASE_TARGET_CONN" target >/dev/null
  local objects tables_json
  objects="$(discover_objects "$CASE_SOURCE_CONN" compat_source source)"
  tables_json="$(jq -c '[.[].name] | map(select(. == "compat_users" or . == "compat_orders"))' <<<"$objects")"
  if [[ "$(jq 'length' <<<"$tables_json")" -lt 2 ]]; then
    CASE_NOTE="MySQL table discovery did not return expected compat tables"
    return 1
  fi
  CASE_DISCOVERY_STATUS="ok"

  local migration_id
  migration_id="$(create_migration "compat mysql $source_version to $target_version" native "$CASE_SOURCE_CONN" compat_source "$CASE_TARGET_CONN" compat_target "$tables_json")"
  poll_migration "$migration_id" 900
  extract_events "$CASE_REPORT_DIR/api-migration-$migration_id.json" "$CASE_REPORT_DIR/migration-events.tsv"
  verify_mysql_counts "$target_name" compat_target
  CASE_MIGRATION_STATUS="ok"

  local backup_id restore_id restore_db
  backup_id="$(create_backup "compat mysql backup $source_version" "$CASE_SOURCE_CONN" compat_source "$DATABRIDGE_BACKUP_DIR")"
  poll_backup "$backup_id" backup 900
  extract_events "$CASE_REPORT_DIR/api-backup-$backup_id.json" "$CASE_REPORT_DIR/backup-events.tsv"
  CASE_BACKUP_STATUS="ok"

  restore_db="compat_restore"
  docker exec "$target_name" mysql -uroot -pcompatpass -e "DROP DATABASE IF EXISTS $restore_db; CREATE DATABASE $restore_db CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; GRANT ALL PRIVILEGES ON \`$restore_db\`.* TO 'compat'@'%'; FLUSH PRIVILEGES;" >/dev/null
  restore_id="$(restore_backup "$backup_id" "$CASE_TARGET_CONN" "$restore_db")"
  poll_backup "$restore_id" restore 900
  extract_events "$CASE_REPORT_DIR/api-restore-$restore_id.json" "$CASE_REPORT_DIR/restore-events.tsv"
  verify_mysql_counts "$target_name" "$restore_db"
  CASE_RESTORE_STATUS="ok"

  CASE_CONCLUSION="passed"
  CASE_NOTE="report: $CASE_REPORT_DIR"
}

wait_mysql_ready() {
  local container="$1"
  local timeout=180
  local start now
  start="$(date +%s)"
  until docker exec "$container" mysqladmin ping -h127.0.0.1 -ucompat -pcompatpass --silent >/dev/null 2>&1; do
    now="$(date +%s)"
    if (( now - start > timeout )); then
      echo "Timed out waiting for MySQL container $container" >&2
      return 1
    fi
    sleep 2
  done
}

verify_mysql_counts() {
  local container="$1"
  local database="$2"
  local users orders
  users="$(docker exec "$container" mysql -h127.0.0.1 -N -ucompat -pcompatpass "$database" -e "SELECT count(*) FROM compat_users;" 2>/dev/null)"
  orders="$(docker exec "$container" mysql -h127.0.0.1 -N -ucompat -pcompatpass "$database" -e "SELECT count(*) FROM compat_orders;" 2>/dev/null)"
  [[ "$users" == "1000" && "$orders" == "1000" ]]
}
