#!/bin/bash
set -e

export GOPROXY=direct

CURRENT_GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
go mod edit -go=$CURRENT_GO_VERSION
go mod tidy
echo "Updated go.mod to Go version: $CURRENT_GO_VERSION"

sudo apt-get update
sudo apt-get install -y gcc-mingw-w64-i686 gcc-multilib

COMMON_LDFLAGS="-s -w -extldflags -static -extldflags -static"

TARGETS=(
  "android arm64"
  "darwin amd64"
  "darwin arm64"
  "freebsd 386"
  "freebsd amd64"
  "freebsd arm64"
  "freebsd arm"
  "linux 386"
  "linux amd64"
  "linux arm64"
  "linux arm 5"
  "linux arm 6"
  "linux arm 7"
  "linux loong64"
  "linux mips64le"
  "linux mips64"
  "linux mipsle"
  "linux mips"
  "linux riscv64"
  "windows 386"
  "windows amd64"
  "windows arm64"
)

NPC_TAR_FILES="conf/npc.conf conf/multi_account.conf"
NPS_TAR_FILES="conf/nps.conf web/views web/static"

SDK_TARGETS=(
  "windows 386 i686-w64-mingw32-gcc"
  "linux 386 gcc"
)

build_binary() {
  local name="$1"
  local os="$2"
  local arch="$3"
  local goarm="$4"
  local ext=""
  [ "$os" = "windows" ] && ext=".exe"
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch GOARM=$goarm \
    go build -ldflags "$COMMON_LDFLAGS" -o "$name$ext" "./cmd/$name/$name.go"
}

package_binary() {
  local name="$1"
  local os="$2"
  local arch="$3"
  local goarm="$4"
  local tar_files="$5"
  local suffix="$6"
  local ext=""
  [ "$os" = "windows" ] && ext=".exe"
  local bin="$name$ext"
  local tarname="${os}_${arch}${goarm:+_v$goarm}_${suffix}.tar.gz"
  tar -czvf "$tarname" $bin $tar_files
  rm -f "$bin"
}

build_all_targets() {
  local name="$1"
  local suffix="$2"
  local targets=("${!3}")
  local tar_files="$4"
  for entry in "${targets[@]}"; do
    IFS=' ' read -r os arch goarm <<< "$entry"
    build_binary "$name" "$os" "$arch" "$goarm"
    package_binary "$name" "$os" "$arch" "$goarm" "$tar_files" "$suffix"
  done
}

build_sdk() {
  rm -rf sdk_*
  for target in "${SDK_TARGETS[@]}"; do
    IFS=' ' read -r os arch cc <<< "$target"
    local folder="sdk_${os}_${arch}"
    mkdir -p "$folder"
    local ext=""
    [ "$os" = "windows" ] && ext=".dll" || ext=".so"
    CGO_ENABLED=1 GOOS=$os GOARCH=$arch CC=$cc \
      go build -buildmode=c-shared -ldflags "$COMMON_LDFLAGS" -o "$folder/npc_sdk$ext" cmd/npc/sdk.go
    cp npc_sdk.h "$folder"/ 2>/dev/null || true
  done
  tar -czvf npc_sdk.tar.gz sdk_*
  rm -rf sdk_*
}

build_sdk
build_all_targets npc "client" TARGETS[@] "$NPC_TAR_FILES"
build_all_targets nps "server" TARGETS[@] "$NPS_TAR_FILES"
