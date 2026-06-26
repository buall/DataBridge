#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

source scripts/compatibility/lib/docker-case.sh
source scripts/compatibility/lib/databridge-api.sh
source scripts/compatibility/cases/postgres.sh
source scripts/compatibility/cases/mysql.sh
source scripts/compatibility/cases/mongo.sh
source scripts/compatibility/cases/neo4j.sh

usage() {
  cat <<'EOF'
Usage:
  ./scripts/compatibility/run.sh postgres [source_version target_version]
  ./scripts/compatibility/run.sh mysql [source_version target_version]
  ./scripts/compatibility/run.sh mongo [source_version target_version]
  ./scripts/compatibility/run.sh neo4j [source_version target_version]
  ./scripts/compatibility/run.sh all

Examples:
  ./scripts/compatibility/run.sh postgres 13 18
  ./scripts/compatibility/run.sh mysql 8.0 8.0
EOF
}

main() {
  local db_type="${1:-}"
  local source_version="${2:-}"
  local target_version="${3:-}"
  if [[ -z "$db_type" || "$db_type" == "-h" || "$db_type" == "--help" ]]; then
    usage
    exit 0
  fi
  if [[ -n "$source_version" && -z "$target_version" ]]; then
    echo "source_version and target_version must be provided together" >&2
    usage
    exit 2
  fi

  print_environment
  ensure_prerequisites
  ensure_docker_network
  ensure_backup_dir
  ensure_databridge
  wait_http_ready "${DATABRIDGE_API_BASE%/api}/api/healthz" 180

  case "$db_type" in
    postgres)
      run_selected_or_matrix "$source_version" "$target_version" run_postgres_case \
        "13 13" "13 18" "18 13" "15 15" "14 16"
      ;;
    mysql)
      run_selected_or_matrix "$source_version" "$target_version" run_mysql_case \
        "5.7 5.7" "5.7 8.0" "8.0 5.7" "8.0 8.0"
      ;;
    mongo)
      run_selected_or_matrix "$source_version" "$target_version" run_mongo_case \
        "5.0 5.0" "5.0 8.0" "8.0 5.0" "6.0 7.0"
      ;;
    neo4j)
      run_selected_or_matrix "$source_version" "$target_version" run_neo4j_case \
        "4.4 4.4" "4.4 5.x" "5.x 5.x"
      ;;
    all)
      if [[ -n "$source_version" || -n "$target_version" ]]; then
        echo "'all' does not accept source/target versions" >&2
        exit 2
      fi
      run_selected_or_matrix "" "" run_postgres_case "13 13" "13 18" "18 13" "15 15" "14 16"
      run_selected_or_matrix "" "" run_mysql_case "5.7 5.7" "5.7 8.0" "8.0 5.7" "8.0 8.0"
      run_selected_or_matrix "" "" run_mongo_case "5.0 5.0" "5.0 8.0" "8.0 5.0" "6.0 7.0"
      run_selected_or_matrix "" "" run_neo4j_case "4.4 4.4" "4.4 5.x" "5.x 5.x"
      ;;
    *)
      echo "Unknown database type: $db_type" >&2
      usage
      exit 2
      ;;
  esac
}

print_environment() {
  echo "System: $(uname -a)"
  echo "User: $(id)"
  echo "Repository: $ROOT_DIR"
  echo "DataBridge API: $DATABRIDGE_API_BASE"
  echo "Docker network: $DATABRIDGE_NETWORK"
  echo "Backup dir: $DATABRIDGE_BACKUP_DIR"
}

ensure_prerequisites() {
  local missing=()
  local command
  for command in git curl jq openssl; do
    command -v "$command" >/dev/null 2>&1 || missing+=("$command")
  done
  if (( ${#missing[@]} > 0 )); then
    install_apt_packages "${missing[@]}"
  fi
  if ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
    install_docker
  fi
  require_commands git curl jq openssl docker
  docker compose version >/dev/null
}

install_apt_packages() {
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "apt-get is required to install missing packages: $*" >&2
    return 1
  fi
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"
}

install_docker() {
  if ! command -v apt-get >/dev/null 2>&1; then
    echo "apt-get is required for automatic Docker installation" >&2
    return 1
  fi
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl gnupg
  install -m 0755 -d /etc/apt/keyrings
  if [[ ! -s /etc/apt/keyrings/docker.gpg ]]; then
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg
  fi
  local codename arch
  codename="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
  arch="$(dpkg --print-architecture)"
  echo "deb [arch=$arch signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $codename stable" >/etc/apt/sources.list.d/docker.list
  apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker >/dev/null 2>&1 || service docker start
}

run_selected_or_matrix() {
  local selected_source="$1"
  local selected_target="$2"
  local runner="$3"
  shift 3
  if [[ -n "$selected_source" ]]; then
    "$runner" "$selected_source" "$selected_target"
    return
  fi
  local pair source target
  for pair in "$@"; do
    read -r source target <<<"$pair"
    "$runner" "$source" "$target"
  done
}

main "$@"
