#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${DIST_DIR:-dist}"
case "$DIST_DIR" in
  /*|[A-Za-z]:/*)
    ;;
  *)
    DIST_DIR="$ROOT_DIR/$DIST_DIR"
    ;;
esac
RELEASE_DIR="${RELEASE_DIR:-$DIST_DIR/release}"
WORK_DIR="$DIST_DIR/.build"
GO_BIN="${GO_BIN:-${GO:-go}}"
LDFLAGS="${LDFLAGS:--s -w}"

DEFAULT_TARGETS="linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64"
CLIENT_TARGETS="${CLIENT_TARGETS:-${TARGETS:-$DEFAULT_TARGETS}}"
SERVER_TARGETS="${SERVER_TARGETS:-${TARGETS:-$DEFAULT_TARGETS}}"

usage() {
  cat <<'EOF'
Usage: ./build.sh [all|client|server|list]

Builds release archives for the common nps/npc targets.

Environment variables:
  DIST_DIR        Output root directory. Default: ./dist
  TARGETS         Override both client and server targets.
  CLIENT_TARGETS  Override client targets only.
  SERVER_TARGETS  Override server targets only.
  GO / GO_BIN     Go executable to use. Default: go
  LDFLAGS         go build -ldflags value. Default: -s -w

Target format:
  linux/amd64 windows/amd64 darwin/arm64
EOF
}

log() {
  printf '==> %s\n' "$*"
}

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required tool: %s\n' "$1" >&2
    exit 1
  fi
}

resolve_go_bin() {
  if command -v "$GO_BIN" >/dev/null 2>&1; then
    GO_BIN="$(command -v "$GO_BIN")"
    return
  fi

  if [[ -x "$GO_BIN" ]]; then
    return
  fi

  if [[ "$GO_BIN" == "go" ]]; then
    local candidate=""
    for candidate in \
      "/c/Program Files/Go/bin/go.exe" \
      "/c/Go/bin/go.exe"
    do
      if [[ -x "$candidate" ]]; then
        GO_BIN="$candidate"
        return
      fi
    done
  fi

  printf 'Missing required tool: %s\n' "$GO_BIN" >&2
  exit 1
}

copy_assets() {
  local stage_dir="$1"
  shift

  local path=""
  local parent=""
  for path in "$@"; do
    parent="$(dirname "$path")"
    mkdir -p "$stage_dir/$parent"
    cp -R "$ROOT_DIR/$path" "$stage_dir/$path"
  done
}

build_archive() {
  local name="$1"
  local suffix="$2"
  local target="$3"
  shift 3
  local assets=("$@")

  local os="${target%/*}"
  local arch="${target#*/}"
  local ext=""
  local binary_name="$name"

  if [[ "$os" == "windows" ]]; then
    ext=".exe"
    binary_name+="$ext"
  fi

  local archive_name="${os}_${arch}_${suffix}.tar.gz"
  local archive_path="$RELEASE_DIR/$archive_name"
  local stage_dir="$WORK_DIR/${name}_${os}_${arch}"

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"

  log "Building $name for $os/$arch"
  (
    cd "$ROOT_DIR"
    env CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      "$GO_BIN" build -trimpath -ldflags "$LDFLAGS" -o "$stage_dir/$binary_name" "./cmd/$name"
  )

  copy_assets "$stage_dir" "${assets[@]}"

  log "Packaging $archive_name"
  (
    cd "$stage_dir"
    tar -czf "$archive_path" "$binary_name" "${assets[@]}"
  )
}

build_group() {
  local name="$1"
  local suffix="$2"
  local targets="$3"
  shift 3
  local assets=("$@")

  local target=""
  for target in $targets; do
    build_archive "$name" "$suffix" "$target" "${assets[@]}"
  done
}

prepare_dirs() {
  mkdir -p "$RELEASE_DIR"
  rm -rf "$WORK_DIR"
  mkdir -p "$WORK_DIR"
}

main() {
  local mode="${1:-all}"

  case "$mode" in
    all|client|server|list)
      ;;
    -h|--help|help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 1
      ;;
  esac

  if [[ "$mode" == "list" ]]; then
    printf 'client targets: %s\n' "$CLIENT_TARGETS"
    printf 'server targets: %s\n' "$SERVER_TARGETS"
    exit 0
  fi

  resolve_go_bin
  require_tool tar

  prepare_dirs

  if [[ "$mode" == "all" || "$mode" == "client" ]]; then
    build_group npc client "$CLIENT_TARGETS" \
      conf/npc.conf \
      conf/multi_account.conf
  fi

  if [[ "$mode" == "all" || "$mode" == "server" ]]; then
    build_group nps server "$SERVER_TARGETS" \
      conf/nps.conf \
      web/views \
      web/static
  fi

  log "Release archives written to $RELEASE_DIR"
}

main "$@"
