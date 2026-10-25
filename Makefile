.DEFAULT_GOAL := help

BIN := dist/guardrails

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

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

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

.PHONY: header
header: ## Render the README and social graphics from docs/assets/src
	docs/assets/render.sh

.PHONY: version
version: ## Set the release version everywhere it's pinned (VERSION=X.Y.Z)
	scripts/bump-version.sh $(VERSION)

.PHONY: snapshot
snapshot: ## Build a local release snapshot with GoReleaser
	goreleaser release --snapshot --clean --skip=sign
