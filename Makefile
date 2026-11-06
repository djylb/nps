GO ?= go
DIST_DIR ?= dist
BIN_DIR ?= $(DIST_DIR)/bin
CGO_ENABLED ?= 0
LDFLAGS ?= -s -w
TARGETS ?=
CLIENT_TARGETS ?=
SERVER_TARGETS ?=

LOCAL_OS := $(shell $(GO) env GOOS)
EXE :=

ifeq ($(LOCAL_OS),windows)
EXE := .exe
endif

ifeq ($(OS),Windows_NT)
BASH := "C:\Program Files\Git\bin\bash.exe"
MKDIR_BIN_CMD = if not exist "$(subst /,\,$(BIN_DIR))" mkdir "$(subst /,\,$(BIN_DIR))"
CLEAN_DIST_CMD = if exist "$(subst /,\,$(DIST_DIR))" rmdir /s /q "$(subst /,\,$(DIST_DIR))"
BUILD_CLIENT_CMD = set "CGO_ENABLED=$(CGO_ENABLED)" && $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/npc$(EXE) ./cmd/npc
BUILD_SERVER_CMD = set "CGO_ENABLED=$(CGO_ENABLED)" && $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nps$(EXE) ./cmd/nps
RELEASE_ALL_CMD = set "DIST_DIR=$(DIST_DIR)" && set "TARGETS=$(TARGETS)" && set "CLIENT_TARGETS=$(CLIENT_TARGETS)" && set "SERVER_TARGETS=$(SERVER_TARGETS)" && set "LDFLAGS=$(LDFLAGS)" && $(BASH) -lc "./build.sh all"
RELEASE_CLIENT_CMD = set "DIST_DIR=$(DIST_DIR)" && set "TARGETS=$(TARGETS)" && set "CLIENT_TARGETS=$(CLIENT_TARGETS)" && set "SERVER_TARGETS=$(SERVER_TARGETS)" && set "LDFLAGS=$(LDFLAGS)" && $(BASH) -lc "./build.sh client"
RELEASE_SERVER_CMD = set "DIST_DIR=$(DIST_DIR)" && set "TARGETS=$(TARGETS)" && set "CLIENT_TARGETS=$(CLIENT_TARGETS)" && set "SERVER_TARGETS=$(SERVER_TARGETS)" && set "LDFLAGS=$(LDFLAGS)" && $(BASH) -lc "./build.sh server"
else
BASH := bash
MKDIR_BIN_CMD = mkdir -p "$(BIN_DIR)"
CLEAN_DIST_CMD = rm -rf "$(DIST_DIR)"
BUILD_CLIENT_CMD = CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/npc$(EXE) ./cmd/npc
BUILD_SERVER_CMD = CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/nps$(EXE) ./cmd/nps
RELEASE_ALL_CMD = DIST_DIR="$(DIST_DIR)" TARGETS="$(TARGETS)" CLIENT_TARGETS="$(CLIENT_TARGETS)" SERVER_TARGETS="$(SERVER_TARGETS)" LDFLAGS="$(LDFLAGS)" $(BASH) ./build.sh all
RELEASE_CLIENT_CMD = DIST_DIR="$(DIST_DIR)" TARGETS="$(TARGETS)" CLIENT_TARGETS="$(CLIENT_TARGETS)" SERVER_TARGETS="$(SERVER_TARGETS)" LDFLAGS="$(LDFLAGS)" $(BASH) ./build.sh client
RELEASE_SERVER_CMD = DIST_DIR="$(DIST_DIR)" TARGETS="$(TARGETS)" CLIENT_TARGETS="$(CLIENT_TARGETS)" SERVER_TARGETS="$(SERVER_TARGETS)" LDFLAGS="$(LDFLAGS)" $(BASH) ./build.sh server
endif

.PHONY: help build build-client build-server test fmt release release-client release-server package ci clean

help:
	@echo "make build          Build npc and nps for the current host into $(BIN_DIR)"
	@echo "make test           Run go test ./..."
	@echo "make fmt            Run go fmt ./..."
	@echo "make release        Build common release archives into $(DIST_DIR)/release"
	@echo "make release-client Build client release archives only"
	@echo "make release-server Build server release archives only"
	@echo "make clean          Remove $(DIST_DIR)"

build: build-client build-server

build-client:
	@$(MKDIR_BIN_CMD)
	@$(BUILD_CLIENT_CMD)

build-server:
	@$(MKDIR_BIN_CMD)
	@$(BUILD_SERVER_CMD)

test:
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

release:
	@$(RELEASE_ALL_CMD)

release-client:
	@$(RELEASE_CLIENT_CMD)

release-server:
	@$(RELEASE_SERVER_CMD)

package: release

ci: test build

clean:
	@$(CLEAN_DIST_CMD)

.DEFAULT_GOAL := build
