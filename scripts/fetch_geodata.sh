#!/usr/bin/env bash
# Downloads geoip.dat and geosite.dat of the latest Loyalsoldier/v2ray-rules-dat
# release into CONF_DIR, checks them against the SHA-256 sums published with
# the release and records the release in geodata.version.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONF_DIR="${CONF_DIR:-$ROOT_DIR/conf}"
GEO_REPO="${GEO_REPO:-Loyalsoldier/v2ray-rules-dat}"

log() {
  printf '==> %s\n' "$*"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

fetch() {
  curl -fsSL --retry 5 --retry-delay 2 --retry-all-errors --connect-timeout 20 "$@"
}

main() {
  mkdir -p "$CONF_DIR"
  local auth=()
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  fi
  local tag
  tag="$(fetch ${auth[@]+"${auth[@]}"} "https://api.github.com/repos/$GEO_REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
  if [[ -z "$tag" ]]; then
    printf 'Cannot determine the latest release of %s\n' "$GEO_REPO" >&2
    exit 1
  fi
  log "Geodata release $tag"

  local name base want got tmp
  for name in geoip.dat geosite.dat; do
    base="https://github.com/$GEO_REPO/releases/download/$tag/$name"
    want="$(fetch "$base.sha256sum" | awk '{print $1}')"
    tmp="$CONF_DIR/$name.download"
    log "Downloading $name"
    fetch -o "$tmp" "$base"
    got="$(sha256_of "$tmp")"
    if [[ "$got" != "$want" ]]; then
      rm -f "$tmp"
      printf '%s: checksum mismatch: got %s, want %s\n' "$name" "$got" "$want" >&2
      exit 1
    fi
    mv "$tmp" "$CONF_DIR/$name"
  done
  printf '%s\n' "$tag" > "$CONF_DIR/geodata.version"
}

main "$@"
