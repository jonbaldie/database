# syntax=docker/dockerfile:1.7

FROM golang:1.26.6-bookworm

# Install development tools and system utilities
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    make \
    procps \
    python3 \
    && rm -rf /var/lib/apt/lists/*

# Configure safe git directory for mounted workspaces
RUN git config --global --add safe.directory /workspace

WORKDIR /workspace

# Pre-cache Go module dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source files
COPY . .

# Pre-build local binary using Makefile
RUN make build

EXPOSE 3306 8080

CMD ["/bin/bash"]
