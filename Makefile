VERSION ?= 0.1.0
BUILD_IDENTITY ?= local
GO ?= go
LDFLAGS = -s -w -X github.com/jonbaldie/database/internal/buildinfo.ProductVersion=$(VERSION) -X github.com/jonbaldie/database/internal/buildinfo.BuildIdentity=$(BUILD_IDENTITY)
MESSGO_VERSION := v0.2.0
MESSGO_MODULE := github.com/quality-gates/messgo/cmd/messgo
MESSGO_RULESET := config/messgo.xml
MESSGO_PATHS := $(shell $(GO) list -f '{{.Dir}}' ./... | tr '\n' ',' | sed 's/,$$//')
MUTAGO_VERSION := v2.7.7

.PHONY: build test vet fmt-check validate-query-explanation quality messgo mutation performance release release-smoke

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

quality: fmt-check vet test build messgo

messgo:
	$(GO) run $(MESSGO_MODULE)@$(MESSGO_VERSION) $(MESSGO_PATHS) text $(MESSGO_RULESET) --ignore-tests

mutation:
	MUTAGO_VERSION=$(MUTAGO_VERSION) ./scripts/mutation-threshold.sh

performance:
	./scripts/performance.sh

release:
	./scripts/build-release.sh

release-smoke:
	./scripts/verify-release.sh
