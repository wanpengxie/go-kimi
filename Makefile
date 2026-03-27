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

.PHONY: prepare-cache build test test-e2e lint fmt ci

prepare-cache:
	mkdir -p $(GOCACHE) $(GOMODCACHE) $(GOPATH) $(GOLANGCI_LINT_CACHE)

build: prepare-cache
	$(GOENV) $(GO) build $(PKGS)

test: prepare-cache
	$(GOENV) $(GO) test $(PKGS)

test-e2e: prepare-cache
	$(GOENV) $(GO) test -tags=e2e $(E2E_PKGS)

lint: prepare-cache
	GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run $(PKGS)

fmt: prepare-cache
	$(GOENV) $(GO) fmt $(PKGS)
	$(GOIMPORTS) -w $$(find . -type f -name '*.go' -not -path './vendor/*')

ci: lint test build
