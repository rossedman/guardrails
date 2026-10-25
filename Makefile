.DEFAULT_GOAL := help

BIN := dist/guardrails
FUZZTIME ?= 30s

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[33m%-10s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into dist/
	go build -trimpath -o $(BIN) ./cmd/guardrails

.PHONY: test
test: ## Run tests with the race detector and coverage
	go test -race -count=1 -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -n 1

.PHONY: cover
cover: test ## Fail if hand-written code is below 90% coverage
	tools/cover.sh coverage.out 90

.PHONY: bench
bench: ## Run benchmarks 6 times into bench.txt, for benchstat
	go test -run='^$$' -bench=. -benchmem -count=6 ./... | tee bench.txt

.PHONY: fuzz
fuzz: ## Run every fuzz target for FUZZTIME (default 30s)
	FUZZTIME=$(FUZZTIME) tools/fuzz.sh

.PHONY: lint
lint: ## Run golangci-lint, the one-command-per-file check and doccheck
	golangci-lint run
	tools/check-cmds.sh
	go run ./tools/doccheck ./...

.PHONY: fmt
fmt: ## Format with gofumpt and goimports
	gofumpt -w .
	goimports -w .

.PHONY: gate
gate: build ## Run Guardrails' own gate on itself
	$(BIN) gate --all --explain

.PHONY: harden
harden: build ## Mutation-test and fuzz the Go changed on this branch
	$(BIN) harden --base origin/main

.PHONY: docs
docs: ## Serve the docs locally
	pip install -q -r docs/requirements.txt
	mkdocs serve

.PHONY: docs-build
docs-build: ## Build the docs site strictly, as CI does
	mkdocs build --strict

.PHONY: check
check: lint cover ## Everything CI checks: lint, tests, coverage, benches, fuzzing, docs, release config
	go test -run='^$$' -bench=. -benchtime=1x ./...
	FUZZTIME=10s tools/fuzz.sh
	go run github.com/goreleaser/goreleaser/v2@latest check
	$(MAKE) docs-build

.PHONY: header
header: ## Render the README and social graphics from docs/assets/src
	docs/assets/render.sh

.PHONY: version
version: ## Set the release version everywhere it's pinned (VERSION=X.Y.Z)
	scripts/bump-version.sh $(VERSION)

.PHONY: snapshot
snapshot: ## Build a local release snapshot with GoReleaser
	goreleaser release --snapshot --clean --skip=sign
