NAME=process-compose
RM=rm
#VERSION = v0.51.0
VERSION = $(shell git describe --abbrev=0)
GIT_REV    ?= $(shell git rev-parse --short HEAD)
DATE       ?= $(shell TZ=UTC0 git show --quiet --date='format-local:%Y-%m-%dT%H:%M:%SZ' --format="%cd")
NUMVER = $(shell echo ${VERSION} | cut -d"v" -f 2)
PKG = github.com/f1bonacc1/${NAME}
SHELL := /usr/bin/env bash
PROJ_NAME := Process Compose
DOCS_DIR  := www/docs/cli
LD_FLAGS := -ldflags="-X ${PKG}/src/config.Version=${VERSION} \
            -X ${PKG}/src/config.CheckForUpdates=false \
            -X ${PKG}/src/config.SelfUpdateEnabled=false \
            -X ${PKG}/src/config.Commit=${GIT_REV} \
            -X ${PKG}/src/config.Date=${DATE} \
            -X '${PKG}/src/config.ProjectName=${PROJ_NAME} 🔥' \
            -X '${PKG}/src/config.RemoteProjectName=${PROJ_NAME} ⚡' \
            -s -w"
ifeq ($(OS),Windows_NT)
	EXT=.exe
	RM = cmd /C del /Q /F
endif

.PHONY: setup test run testrace docs schema openapi

buildrun: build run

setup:
	go mod download

ci: setup build testrace lint build-nix

openapi: ## Generate the HTTP API OpenAPI document from the registered operations
	go run ./scripts/generate-openapi.go

.PHONY: check-openapi
check-openapi: ## Verify the committed OpenAPI document matches the registered operations
	go test ./src/api -run '^TestOpenAPIDocumentIsCurrent$$' -count=1

.PHONY: check-openapi-contract
check-openapi-contract: check-openapi ## Verify a freshly built binary against the generated OpenAPI document
	PC_OPENAPI_LIVE_CONFORMANCE=1 PC_SCHEMATHESIS_CONFORMANCE=1 go test ./src/api -count=1

build:
	CGO_ENABLED=0 go build -o bin/${NAME}${EXT} ${LD_FLAGS} ./

build-nix:
	nix build .

nixver:
	sed -i 's/version = ".*"/version = "${NUMVER}"/' default.nix

nix-update-hash:
	./scripts/update-vendor-hash.sh

build-pi:
	GOOS=linux GOARCH=arm go build ${LD_FLAGS} -o bin/${NAME}-linux-arm  ./

compile:
	for arch in amd64 386 arm64 arm; do \
		GOOS=linux GOARCH=$$arch go build ${LD_FLAGS} -o bin/${NAME}-linux-$$arch  ./ ; \
	done;

	for arch in amd64 arm64; do \
		GOOS=darwin GOARCH=$$arch go build ${LD_FLAGS} -o bin/${NAME}-darwin-$$arch  ./ ; \
	done;

	for arch in amd64 arm64; do \
		GOOS=windows GOARCH=$$arch go build ${LD_FLAGS} -o bin/${NAME}-windows-$$arch.exe  ./ ; \
	done;

test:
	go test -cover ./...

testrace:
	go test -race ./...

testrace-clean:
	go clean -testcache && go test -race ./...

coverhtml:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

run: build
	PC_DEBUG_MODE=1 ./bin/${NAME}${EXT} -e .env

clean:
	$(RM) bin/${NAME}*

.PHONY: release snapshot
release:
	@echo "Direct local releases are disabled for this fork." >&2
	@echo "Push an eligible vMAJOR.MINOR.PATCH+gkzeN tag to run the tag-triggered GitHub Actions draft release workflow." >&2
	@exit 1

snapshot:
	goreleaser release --snapshot --clean

github-workflows:
	act -W ./.github/workflows/go.yml -j build --matrix os:ubuntu-latest
	act -W ./.github/workflows/nix.yml -j build

docs: build
	./bin/process-compose docs ${DOCS_DIR}
	for f in ${DOCS_DIR}/*.md ; do sed -i 's/${USER}/<user>/g; s|${TMPDIR}|/tmp/|g; s/process-compose-[0-9]\+.sock/process-compose-<pid>.sock/g' $$f ; done

schema:
	./bin/process-compose schema ./schemas/process-compose-schema.json

lint: golangci-lint
	./bin/golangci-lint run --show-stats -c .golangci.yaml

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	test -s $(LOCALBIN)/golangci-lint || \
	GOBIN=$(LOCALBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
