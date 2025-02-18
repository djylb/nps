SOURCE_FILES?=./...
TEST_PATTERN?=.
TEST_OPTIONS?=

export PATH := ./bin:$(PATH)
export GO111MODULE := on
export GOPROXY := https://goproxy.io

GIT_VERSION=$(shell git describe --tags --abbrev=14 --match "v[0-9]*" 2>/dev/null | sed 's/^v//')
# 检查 GIT_VERSION 是否为空
ifeq ($(GIT_VERSION),)
$(error GIT_VERSION is null. Please ensure you have a valid Git version.)
endif
LDFLAGS="-s -w -extldflags -static -X 'ehang.io/nps/lib/version.VERSION=$(GIT_VERSION)'"

# Build a beta version of goreleaser
build:
	go build -trimpath  -ldflags $(LDFLAGS) cmd/nps/nps.go
	go build -trimpath  -ldflags $(LDFLAGS) cmd/npc/npc.go
.PHONY: build

# Install all the build and lint dependencies
setup:
	curl -sfL https://install.goreleaser.com/github.com/golangci/golangci-lint.sh | sh
	curl -L https://git.io/misspell | sh
	go mod download
.PHONY: setup

# Run all the tests
test:
	go test $(TEST_OPTIONS) -failfast -race -coverpkg=./... -covermode=atomic -coverprofile=coverage.txt $(SOURCE_FILES) -run $(TEST_PATTERN) -timeout=2m
.PHONY: test

# Run all the tests and opens the coverage report
cover: test
	go tool cover -html=coverage.txt
.PHONY: cover

# gofmt and goimports all go files
fmt:
	find . -name '*.go' -not -wholename './vendor/*' | while read -r file; do gofmt -w -s "$$file"; goimports -w "$$file"; done
.PHONY: fmt

# Run all the linters
lint:
	# TODO: fix tests and lll issues
	./bin/golangci-lint run --tests=false --enable-all --disable=lll ./...
	./bin/misspell -error **/*
.PHONY: lint

# Clean go.mod
go-mod-tidy:
	@go mod tidy -v
	@git diff HEAD
	@git diff-index --quiet HEAD
.PHONY: go-mod-tidy

# Run all the tests and code checks
ci: build test lint go-mod-tidy
.PHONY: ci

# Generate the static documentation
static:
	@hugo --enableGitInfo --source www
.PHONY: static

# Show to-do items per file.
todo:
	@grep \
		--exclude-dir=vendor \
		--exclude-dir=node_modules \
		--exclude=Makefile \
		--text \
		--color \
		-nRo -E ' TODO:.*|SkipNow' .
.PHONY: todo

clean:
	rm npc nps
.PHONY: clean

.PHONY: docker-nps
docker-nps:
	docker build --build-arg LDFLAGS=$(LDFLAGS) -f  Dockerfile.nps -t nps:$(GIT_VERSION) .

.PHONY: docker-npc
docker-npc:
	docker build --build-arg LDFLAGS=$(LDFLAGS) -f  Dockerfile.npc -t npc:$(GIT_VERSION) .

.DEFAULT_GOAL := build
