VERSION ?= 0.1.0
BUILD_IDENTITY ?= local
GO ?= go
LDFLAGS = -s -w -X github.com/jonbaldie/database/internal/buildinfo.ProductVersion=$(VERSION) -X github.com/jonbaldie/database/internal/buildinfo.BuildIdentity=$(BUILD_IDENTITY)

.PHONY: build test vet fmt-check validate-query-explanation quality mutation

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

quality: fmt-check vet test build

mutation:
	./scripts/mutation-threshold.sh
