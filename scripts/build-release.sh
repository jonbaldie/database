#!/usr/bin/env sh
set -eu
mkdir -p dist
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o dist/database-darwin-arm64 ./cmd/database
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/database-linux-amd64 ./cmd/database
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o dist/database-linux-arm64 ./cmd/database
