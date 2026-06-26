#!/usr/bin/env bash

set -Eeuo pipefail

run_neo4j_case() {
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
  local source_image_version target_image_version
  source_image_version="$(neo4j_image_version "$source_version")"
  target_image_version="$(neo4j_image_version "$target_version")"
  local slug source_name target_name source_volume target_volume
  slug="$(case_slug neo4j "$source_version" "$target_version")"
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
    matrix_update "Neo4j" "$CASE_SOURCE_VERSION" "$CASE_TARGET_VERSION" "$CASE_CONNECTION_STATUS" "$CASE_DISCOVERY_STATUS" "$CASE_MIGRATION_STATUS" "$CASE_BACKUP_STATUS" "$CASE_RESTORE_STATUS" "$CASE_CONCLUSION" "$CASE_NOTE"
    return "$exit_code"
  }
  trap cleanup EXIT

  echo "Running Neo4j compatibility case $source_version -> $target_version"
  docker_case_preclean "$source_name" "$source_volume"
  docker_case_preclean "$target_name" "$target_volume"

  run_neo4j_container "$source_name" "$source_volume" "$source_image_version"
  run_neo4j_container "$target_name" "$target_volume" "$target_image_version"
  wait_neo4j_ready "$source_name"
  wait_neo4j_ready "$target_name"
  verify_neo4j_apoc "$source_name" || {
    CASE_NOTE="APOC export procedure unavailable on source; toolchain/configuration issue"
    CASE_CONCLUSION="conditional"
    return 1
  }
  docker cp scripts/compatibility/seed/neo4j.cypher "$source_name:/tmp/neo4j.cypher"
  docker exec "$source_name" cypher-shell -u neo4j -p compatpass -f /tmp/neo4j.cypher

  api_init
  local source_payload target_payload
  source_payload="$(db_connection_payload Neo4j "compat neo4j $source_version source" "$source_name" 7687 neo4j compatpass disable)"
  target_payload="$(db_connection_payload Neo4j "compat neo4j $target_version target" "$target_name" 7687 neo4j compatpass disable)"
  CASE_SOURCE_CONN="$(create_connection "$source_payload" source)"
  CASE_TARGET_CONN="$(create_connection "$target_payload" target)"
  CASE_CONNECTION_STATUS="ok"

  discover_databases "$CASE_SOURCE_CONN" source >/dev/null
  discover_objects "$CASE_SOURCE_CONN" neo4j source >/dev/null
  CASE_DISCOVERY_STATUS="ok"

  local migration_id
  migration_id="$(create_migration "compat neo4j $source_version to $target_version" native "$CASE_SOURCE_CONN" neo4j "$CASE_TARGET_CONN" neo4j '["neo4j"]')"
  poll_migration "$migration_id" 900
  extract_events "$CASE_REPORT_DIR/api-migration-$migration_id.json" "$CASE_REPORT_DIR/migration-events.tsv"
  verify_neo4j_counts "$target_name"
  CASE_MIGRATION_STATUS="ok"

  local backup_id restore_id
  backup_id="$(create_backup "compat neo4j backup $source_version" "$CASE_SOURCE_CONN" neo4j "$DATABRIDGE_BACKUP_DIR")"
  poll_backup "$backup_id" backup 900
  extract_events "$CASE_REPORT_DIR/api-backup-$backup_id.json" "$CASE_REPORT_DIR/backup-events.tsv"
  CASE_BACKUP_STATUS="ok"

  clear_neo4j_graph "$target_name"
  restore_id="$(restore_backup "$backup_id" "$CASE_TARGET_CONN" neo4j)"
  poll_backup "$restore_id" restore 900
  extract_events "$CASE_REPORT_DIR/api-restore-$restore_id.json" "$CASE_REPORT_DIR/restore-events.tsv"
  verify_neo4j_counts "$target_name"
  CASE_RESTORE_STATUS="ok"

  CASE_CONCLUSION="passed"
  CASE_NOTE="report: $CASE_REPORT_DIR"
}

neo4j_image_version() {
  case "$1" in
    5.x) echo "5" ;;
    *) echo "$1" ;;
  esac
}

run_neo4j_container() {
  local name="$1"
  local volume="$2"
  local version="$3"
  docker run -d --name "$name" --network "$DATABRIDGE_NETWORK" -v "$volume:/data" \
    -e NEO4J_AUTH=neo4j/compatpass \
    -e NEO4J_PLUGINS='["apoc"]' \
    -e NEO4J_apoc_export_file_enabled=true \
    -e NEO4J_apoc_import_file_use__neo4j__config=false \
    -e NEO4J_dbms_security_procedures_unrestricted=apoc.* \
    -e NEO4J_dbms_security_procedures_allowlist=apoc.* \
    "neo4j:$version" >/dev/null
}

wait_neo4j_ready() {
  local container="$1"
  local timeout=240
  local start now
  start="$(date +%s)"
  until docker exec "$container" cypher-shell -u neo4j -p compatpass "RETURN 1;" >/dev/null 2>&1; do
    now="$(date +%s)"
    if (( now - start > timeout )); then
      echo "Timed out waiting for Neo4j container $container" >&2
      return 1
    fi
    sleep 3
  done
}

verify_neo4j_apoc() {
  local container="$1"
  docker exec "$container" cypher-shell -u neo4j -p compatpass "CALL apoc.export.cypher.all(null, {stream: true}) YIELD cypherStatements RETURN count(cypherStatements);" >/dev/null
}

clear_neo4j_graph() {
  local container="$1"
  docker exec "$container" cypher-shell -u neo4j -p compatpass "MATCH (n) DETACH DELETE n;" >/dev/null
}

verify_neo4j_counts() {
  local container="$1"
  local nodes relationships
  nodes="$(docker exec "$container" cypher-shell -u neo4j -p compatpass --format plain "MATCH (n) RETURN count(n) AS count;" | tail -n 1 | tr -d '\"[:space:]')"
  relationships="$(docker exec "$container" cypher-shell -u neo4j -p compatpass --format plain "MATCH ()-[r]->() RETURN count(r) AS count;" | tail -n 1 | tr -d '\"[:space:]')"
  [[ "$nodes" == "2000" && "$relationships" == "1001" ]]
}
