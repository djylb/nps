#!/bin/bash
# Writes DIST/manifest.json, the signed release manifest nps and npc update
# themselves from, for the release archives of build.assets.sh and
# build.assets.old.sh in DIST.
#
# Usage: UPDATE_SIGNING_KEY=... scripts/release_manifest.sh VERSION DIST
#        UPDATE_SIGNING_KEY=... scripts/release_manifest.sh --renew DIST
#
# --renew signs DIST/manifest.json again with a new expiry and the rules of
# release/update-rules.json, unchanged otherwise.
#
# release/update-rules.json is the one source of the upgrade rules. The serial
# is the signing time, so it grows across releases without looking at the last
# manifest.
#
# UPDATE_SIGNING_KEY is the private key written by "selfupdate keygen". The
# manifest is checked against ReleaseSigningKey in lib/install/update.go, the
# key the released programs trust. SELFUPDATE names the selfupdate tool
# (default: selfupdate on PATH); MANIFEST_EXPIRES its validity (default 45
# days), after which it has to be signed again.
set -euo pipefail

version="$1"
dist="$2"
tool="${SELFUPDATE:-selfupdate}"
expires="${MANIFEST_EXPIRES:-1080h}"
root="$(cd "$(dirname "$0")/.." && pwd)"

if [ -z "${UPDATE_SIGNING_KEY:-}" ]; then
  echo "UPDATE_SIGNING_KEY is not set" >&2
  exit 1
fi
pub=$(sed -n 's/^const ReleaseSigningKey = "\(.*\)"$/\1/p' "$root/lib/install/update.go")
if [ -z "$pub" ]; then
  echo "ReleaseSigningKey in lib/install/update.go is empty" >&2
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
printf '%s\n' "$UPDATE_SIGNING_KEY" > "$work/release.key"
chmod 600 "$work/release.key"
manifest="$work/manifest.json"
rules="$root/release/update-rules.json"
serial=$(date -u +%s)

if [ "$version" = "--renew" ]; then
  "$tool" verify -pub "$pub" -in "$dist/manifest.json" -out "$manifest"
  jq --slurpfile r "$rules" '.baseline = $r[0].baseline | .rules = ($r[0].rules // []) | .facts = ($r[0].facts // {})' \
    "$manifest" > "$manifest.new"
  mv "$manifest.new" "$manifest"
  "$tool" sign -serial "$serial" -key "$work/release.key" -expires "$expires" -in "$manifest" -out "$dist/manifest.json"
  "$tool" verify -pub "$pub" -in "$dist/manifest.json" > /dev/null
  echo "Renewed $dist/manifest.json"
  exit 0
fi

cp "$rules" "$manifest"

shopt -s nullglob
count=0
for f in "$dist"/*_server.tar.gz "$dist"/*_client.tar.gz "$dist"/*_server_old.tar.gz "$dist"/*_client_old.tar.gz; do
  # <os>_<arch>[_<variant>]_<server|client>[_old].tar.gz
  base=$(basename "$f" .tar.gz)
  # the Android libraries hold no program to update
  case "$base" in
    android_libs_*) continue ;;
  esac
  variant=""
  case "$base" in
    *_old) variant="win7"; base="${base%_old}" ;;
  esac
  kind="${base##*_}"
  base="${base%_*}"
  os="${base%%_*}"
  rest="${base#*_}"
  arch="${rest%%_*}"
  if [ "$rest" != "$arch" ]; then
    variant="${rest#*_}"
  fi
  case "$kind" in
    server) bin=nps ;;
    client) bin=npc ;;
    *) echo "unexpected archive $f" >&2; exit 1 ;;
  esac
  member="$bin"
  [ "$os" = "windows" ] && member="$bin.exe"
  # must match artifactName in lib/install/update.go
  name="$bin"
  [ -n "$variant" ] && name="$bin-$variant"
  "$tool" artifact -name "$name" -os "$os" -arch "$arch" -member "$member" \
    -into "$manifest" -version "$version" "$f"
  count=$((count + 1))
done
if [ "$count" -eq 0 ]; then
  echo "no release archives in $dist" >&2
  exit 1
fi

"$tool" sign -serial "$serial" -key "$work/release.key" -expires "$expires" -in "$manifest" -out "$dist/manifest.json"
"$tool" verify -pub "$pub" -in "$dist/manifest.json" -dist "$dist"
echo "Signed $dist/manifest.json with $count artifacts"
