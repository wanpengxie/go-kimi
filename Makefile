SHELL := /usr/bin/env bash

GO ?= go
PKGS := ./...
E2E_PKGS := ./test/e2e/...
CACHE_DIR ?= .cache
GOCACHE ?= $(abspath $(CACHE_DIR)/go-build)
GOMODCACHE ?= $(abspath $(CACHE_DIR)/go-mod)
GOPATH ?= $(abspath $(CACHE_DIR)/go-path)
GOLANGCI_LINT_CACHE ?= $(abspath $(CACHE_DIR)/golangci-lint)
GOENV := GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) GOPATH=$(GOPATH)
GOIMPORTS := $(GOENV) $(GO) run golang.org/x/tools/cmd/goimports@v0.30.0
GOLANGCI_LINT := $(GOENV) $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.63.0
COVERPKG := $(shell $(GO) list ./... | paste -sd, -)

.PHONY: prepare-cache build test test-coverage test-e2e test-e2e-live lint fmt ci

prepare-cache:
	mkdir -p $(GOCACHE) $(GOMODCACHE) $(GOPATH) $(GOLANGCI_LINT_CACHE)

build: prepare-cache
	$(GOENV) $(GO) build $(PKGS)

test: prepare-cache
	$(GOENV) $(GO) test $(PKGS)

test-coverage: prepare-cache
	$(GOENV) $(GO) test -coverprofile=coverage.out -coverpkg=$(COVERPKG) $(PKGS)
	$(GOENV) $(GO) tool cover -func=coverage.out | tail -n 1

test-e2e: prepare-cache
	$(GOENV) $(GO) test -tags=e2e $(E2E_PKGS)

test-e2e-live: prepare-cache
	@set -e; \
	if command -v direnv >/dev/null 2>&1 && [[ -f .envrc ]]; then \
		direnv allow . >/dev/null 2>&1 || true; \
		if ! direnv exec . bash -lc '[[ -n "$${KIMI_API_KEY:-}" ]]'; then \
			echo "KIMI_API_KEY is not set in direnv (.envrc), skipping live e2e tests."; \
			exit 0; \
		fi; \
		direnv exec . env $(GOENV) $(GO) test -tags=e2e_live $(E2E_PKGS); \
		exit 0; \
	fi; \
	if [[ -f .env.local ]]; then \
		set -a; \
		source .env.local; \
		set +a; \
	fi; \
	if [[ -z "$${KIMI_API_KEY:-}" ]]; then \
		echo "KIMI_API_KEY is not set, skipping live e2e tests."; \
		exit 0; \
	fi; \
	$(GOENV) $(GO) test -tags=e2e_live $(E2E_PKGS)

lint: prepare-cache
	GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run $(PKGS)

fmt: prepare-cache
	$(GOENV) $(GO) fmt $(PKGS)
	$(GOIMPORTS) -w $$(find . -type f -name '*.go' -not -path './vendor/*')

ci: lint test build
