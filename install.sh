#!/bin/sh
# install.sh — Simple installer for nps and npc
# Usage:
#   ./install.sh [mode] [version]
# mode: npc | nps | all (default all)
# version: release tag (default latest)

# Must run as root
if command -v id >/dev/null 2>&1; then
  if [ "$(id -u)" -ne 0 ]; then
    echo "Error: Please run as root or use sudo." >&2
    exit 1
  fi
fi

set -e

has() { command -v "$1" >/dev/null 2>&1; }

# Pre-flight checks: ensure all external commands we rely on are present
for cmd in uname tar; do
  if ! has "$cmd"; then
    echo "Error: missing required command: $cmd" >&2
    exit 1
  fi
done
if ! has curl && ! has wget && ! has uclient-fetch; then
  echo "Error: need curl or wget or uclient-fetch" >&2
  exit 1
fi

# Tunables for downloaders (optional)
CONNECT_TIMEOUT="${NPS_CONNECT_TIMEOUT:-10}"
if [ "${NPS_INSECURE:-0}" = "1" ]; then
  CURL_INSECURE="--insecure"
  WGET_INSECURE="--no-check-certificate"
  UCLIENT_INSECURE="--no-check-certificate"
else
  CURL_INSECURE=""
  WGET_INSECURE=""
  UCLIENT_INSECURE=""
fi
if [ "${NPS_IPV4:-0}" = "1" ]; then
  CURL_IP="-4"
  WGET_IP="-4"
else
  CURL_IP=""
  WGET_IP=""
fi

# Fix one fetch tool
if has curl; then
  FETCH_TOOL="curl"
elif has wget; then
  FETCH_TOOL="wget"
else
  FETCH_TOOL="uclient-fetch"
fi

# Safe cleanup
TMP_DIRS=""
cleanup() {
  for d in $TMP_DIRS; do
    case "$d" in
      /tmp/*)
        [ -n "$d" ] && [ "$d" != "/" ] && [ -d "$d" ] && rm -rf "$d"
        ;;
    esac
  done
}
trap cleanup 0 INT TERM

INSTALL_MODE="${1:-${NPS_INSTALL_MODE:-all}}"
INSTALL_VERSION="${2:-${NPS_INSTALL_VERSION:-latest}}"
INSTALL_DIR="${3:-${NPS_INSTALL_DIR:-}}"

# Validate mode
case "$INSTALL_MODE" in
  npc|nps|all) ;;
  *)
    echo "Error: unsupported mode: $INSTALL_MODE" >&2
    exit 1
    ;;
esac

echo "Mode: $INSTALL_MODE"

USE_CF_LATEST=0

# Fetch latest version if unspecified
if [ "$INSTALL_VERSION" = "latest" ]; then
  echo "Get latest version..."
  if has grep && has sed; then
    API_URL="https://api.github.com/repos/djylb/nps/releases/latest"
    if has curl; then
      RAW_JSON=$(curl -sSLf "$API_URL" || true)
    else
      RAW_JSON=$(wget -qO- "$API_URL" || true)
    fi

    INSTALL_VERSION=$(printf '%s' "$RAW_JSON" \
      | grep -m1 '"tag_name"' \
      | sed 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/')

    if [ -z "$INSTALL_VERSION" ]; then
      echo "Warn: failed to detect version from GitHub API, will use CDN @latest." >&2
      USE_CF_LATEST=1
      INSTALL_VERSION=latest
    fi
  else
    echo "No grep/sed; use CDN @latest." >&2
    USE_CF_LATEST=1
    INSTALL_VERSION=latest
  fi
fi

if [ "$USE_CF_LATEST" -eq 1 ]; then
  echo "Version: latest (CDN @latest fallback)"
else
  echo "Version: $INSTALL_VERSION"
fi

# Determine OS
OS="$(uname -s)"
case "$OS" in
  Linux)   OS=linux;;
  Darwin)  OS=darwin;;
  FreeBSD) OS=freebsd;;
  *)
    echo "Error: OS not supported: $OS" >&2
    exit 1
    ;;
esac

# Determine ARCH
RAW_ARCH="$(uname -m)"
case "$RAW_ARCH" in
  x86_64|amd64)      ARCH=amd64;;
  i?86)              ARCH=386;;
  aarch64|arm64)     ARCH=arm64;;
  armv7*|armv7l)     ARCH=arm_v7;;
  armv6*|armv6l)     ARCH=arm_v6;;
  armv5*|armv5l)     ARCH=arm_v5;;
  arm)               ARCH=arm;;
  mips64le)          ARCH=mips64le;;
  mips64)            ARCH=mips64;;
  mipsle)            ARCH=mipsle;;
  mips)              ARCH=mips;;
  loongarch64)       ARCH=loong64;;
  riscv64)           ARCH=riscv64;;
  *)
    echo "Error: ARCH not supported: $RAW_ARCH" >&2
    exit 1
    ;;
esac

echo "Detected OS/ARCH: $OS/$ARCH"

# MIPS float ABI detection
if [ "$ARCH" = "mips" ] || [ "$ARCH" = "mipsle" ]; then
  if has file; then
    out="$(file /bin/sh 2>/dev/null || true)"
    case "$out" in
      *hard[-\ ]float*)
        echo "Use hard-float for $ARCH"
        ;;
      *)
        ARCH="${ARCH}_softfloat"
        echo "Use soft-float, new ARCH: $ARCH"
        ;;
    esac
  else
    ARCH="${ARCH}_softfloat"
    echo "No 'file' tool; default to soft-float: $ARCH"
  fi
fi

# Extraction
tar_extract() {
  f="$1"

  if tar -xzf "$f" 2>/dev/null; then
    return 0
  fi

  if tar xf "$f" 2>/dev/null; then
    return 0
  fi

  if has gzip; then
    if gzip -dc "$f" | tar xf -; then
      return 0
    fi
  fi

  if has gunzip; then
    if gunzip -c "$f" | tar xf -; then
      return 0
    fi
  fi

  return 1
}

# Download helper
download() {
  NAME=$1
  FILE="${OS}_${ARCH}_${NAME}.tar.gz"

  if [ "$USE_CF_LATEST" -eq 1 ]; then
    URLS="
      https://cdn.jsdelivr.net/gh/djylb/nps-mirror@latest/${FILE}
      https://fastly.jsdelivr.net/gh/djylb/nps-mirror@latest/${FILE}
      https://github.com/djylb/nps/releases/latest/download/${FILE}
    "
  else
    URLS="
      https://github.com/djylb/nps/releases/download/${INSTALL_VERSION}/${FILE}
      https://cdn.jsdelivr.net/gh/djylb/nps-mirror@${INSTALL_VERSION}/${FILE}
      https://fastly.jsdelivr.net/gh/djylb/nps-mirror@${INSTALL_VERSION}/${FILE}
    "
  fi

  # Decide workdir
  if [ -n "$INSTALL_DIR" ]; then
    mkdir -p "$INSTALL_DIR"
    WORKDIR="$INSTALL_DIR"
  else
    if has mktemp; then
      TMPD=$(mktemp -d 2>/dev/null || mktemp -d -t nps-install.XXXXXX)
    else
      TS="$(date +%s 2>/dev/null || echo 0)"
      TMPD="/tmp/nps-$$.$TS"
      mkdir -p "$TMPD"
    fi
    TMP_DIRS="$TMP_DIRS $TMPD"
    WORKDIR="$TMPD"
  fi

  cd "$WORKDIR"

  success=0
  for u in $URLS; do
    echo "Trying $u" >&2

    rm -f -- "./$FILE"

    case "$FETCH_TOOL" in
      curl)
        if curl $CURL_IP -sfSL $CURL_INSECURE --connect-timeout "$CONNECT_TIMEOUT" -o "$FILE" "$u"; then
          success=1
        fi
        ;;
      wget)
        if wget $WGET_IP -q $WGET_INSECURE -T "$CONNECT_TIMEOUT" -O "$FILE" "$u"; then
          success=1
        fi
        ;;
      uclient-fetch)
        if uclient-fetch -q $UCLIENT_INSECURE -T "$CONNECT_TIMEOUT" -O "$FILE" "$u"; then
          success=1
        fi
        ;;
    esac

    if [ "$success" -eq 1 ] && [ -s "$FILE" ]; then
      break
    fi
  done

  if [ "$success" -ne 1 ] || [ ! -f "$FILE" ]; then
    echo "Error: Download failed for all URLs:" >&2
    for u in $URLS; do
      echo "  - $u" >&2
    done
    exit 1
  fi

  if ! tar_extract "$FILE"; then
    echo "Error: failed to extract $FILE" >&2
    exit 1
  fi

  if [ -n "$INSTALL_DIR" ]; then
    rm -f -- "./$FILE"
    printf '%s\n' "$INSTALL_DIR"
  else
    printf '%s\n' "$WORKDIR"
  fi
}

# Install NPC (client)
install_npc() {
  SRC=$(download client)
  echo "Install npc..."

  if [ -n "$INSTALL_DIR" ]; then
    echo "npc installed in $INSTALL_DIR"
    return
  fi

  if [ -x "$SRC/npc" ]; then
    if cp -f "$SRC/npc" /usr/bin/npc 2>/dev/null; then
      chmod 755 /usr/bin/npc
    else
      mkdir -p /usr/local/bin
      cp -f "$SRC/npc" /usr/local/bin/npc
      chmod 755 /usr/local/bin/npc
    fi
  else
    echo "Error: 'npc' binary not found in $SRC" >&2
    exit 1
  fi

  mkdir -p /etc/nps/conf
  if [ ! -f /etc/nps/conf/npc.conf ]; then
    cp "$SRC/conf/npc.conf" /etc/nps/conf/npc.conf 2>/dev/null || true
  else
    cp -f "$SRC/conf/npc.conf" /etc/nps/conf/npc.conf.default 2>/dev/null || true
  fi
  if [ ! -f /etc/nps/conf/multi_account.conf ]; then
    cp "$SRC/conf/multi_account.conf" /etc/nps/conf/multi_account.conf 2>/dev/null || true
  fi
  echo "npc done"
}

# Install NPS (server)
install_nps() {
  SRC=$(download server)
  echo "Install nps..."

  if [ -n "$INSTALL_DIR" ]; then
    echo "nps installed in $INSTALL_DIR"
    return
  fi

  if [ -x "$SRC/nps" ]; then
    if cp -f "$SRC/nps" /usr/bin/nps 2>/dev/null; then
      chmod 755 /usr/bin/nps
    else
      mkdir -p /usr/local/bin
      cp -f "$SRC/nps" /usr/local/bin/nps
      chmod 755 /usr/local/bin/nps
    fi
  else
    echo "Error: 'nps' binary not found in $SRC" >&2
    exit 1
  fi

  mkdir -p /etc/nps/conf /etc/nps/web
  if [ ! -f /etc/nps/conf/nps.conf ]; then
    cp "$SRC/conf/nps.conf" /etc/nps/conf/nps.conf 2>/dev/null || true
  else
    cp -f "$SRC/conf/nps.conf" /etc/nps/conf/nps.conf.default 2>/dev/null || true
  fi
  cp -rf "$SRC/web/"* /etc/nps/web/ 2>/dev/null || true
  echo "nps done"
}

# Run installation per mode
case "$INSTALL_MODE" in
  npc) install_npc ;;
  nps) install_nps ;;
  all) install_npc; install_nps ;;
esac

echo "All done"

