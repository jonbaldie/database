VERSION ?= 0.2.6
BUILD_IDENTITY ?= local
GO ?= go
LDFLAGS = -s -w -X github.com/jonbaldie/database/internal/buildinfo.ProductVersion=$(VERSION) -X github.com/jonbaldie/database/internal/buildinfo.BuildIdentity=$(BUILD_IDENTITY)
MESSGO_VERSION := v0.2.0
MESSGO_MODULE := github.com/quality-gates/messgo/cmd/messgo
MESSGO_RULESET := config/messgo.xml
GOVULNCHECK_VERSION := v1.1.4
GOVULNCHECK_MODULE := golang.org/x/vuln/cmd/govulncheck
GOCYCLO_VERSION := v0.6.0
INEFFASSIGN_VERSION := v0.2.0
MESSGO_PATHS := $(shell $(GO) list -f '{{.Dir}}' ./... | tr '\n' ',' | sed 's/,$$//')

.PHONY: build test vet fmt-check validate-query-explanation quality messgo vulncheck goreportcard mutation performance release release-smoke

build:
	mkdir -p bin
	$(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/database ./cmd/database

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$($(GO)fmt -l .)" || (echo 'gofmt required:'; $(GO)fmt -l .; exit 1)

validate-query-explanation:
	./scripts/validate-query-explanation-schema.sh

quality: fmt-check vet test build messgo vulncheck

messgo:
	$(GO) run $(MESSGO_MODULE)@$(MESSGO_VERSION) $(MESSGO_PATHS) text $(MESSGO_RULESET) --ignore-tests

vulncheck:
	$(GO) run $(GOVULNCHECK_MODULE)@$(GOVULNCHECK_VERSION) ./...

goreportcard:
	@tool_directory="$$(mktemp -d)"; \
	trap 'rm -rf "$$tool_directory"' EXIT; \
	GOBIN="$$tool_directory" $(GO) install github.com/fzipp/gocyclo/cmd/gocyclo@$(GOCYCLO_VERSION); \
	GOBIN="$$tool_directory" $(GO) install github.com/gordonklaus/ineffassign@$(INEFFASSIGN_VERSION); \
	GOREPORTCARD_GOCYCLO="$$tool_directory/gocyclo" GOREPORTCARD_INEFFASSIGN="$$tool_directory/ineffassign" python3 scripts/goreportcard.py

mutation:
	./scripts/mutation-threshold.sh

performance:
	./scripts/performance.sh

release:
	./scripts/build-release.sh

release-smoke:
	./scripts/verify-release.sh
