FROM golang:1.26-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    ffmpeg \
    chromium \
    && rm -rf /var/lib/apt/lists/*

RUN GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
RUN GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@v1.7.0