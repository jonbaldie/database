# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26.6-bookworm AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.2.6
ARG BUILD_IDENTITY=release
ARG SOURCE_DATE_EPOCH=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN test "$SOURCE_DATE_EPOCH" -ge 0
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath -buildvcs=false \
    -ldflags="-s -w -X github.com/jonbaldie/database/internal/buildinfo.ProductVersion=${VERSION} -X github.com/jonbaldie/database/internal/buildinfo.BuildIdentity=${BUILD_IDENTITY}" \
    -o /out/database ./cmd/database

FROM scratch
COPY --from=build /out/database /database
VOLUME ["/data"]
EXPOSE 3306 8080
ENTRYPOINT ["/database"]
