BINARY  := jabledownloader
VERSION ?= dev
IMAGE   := jooservices/go-jabledownloader

GOFLAGS := CGO_ENABLED=0

.PHONY: build run fmt vet lint test cover cover-html ci docker-build docker-run docker-test clean

build: ## Build the CLI binary
	$(GOFLAGS) go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/jabledownloader

run: ## Run the CLI (args via ARGS="get jur-001")
	go run ./cmd/jabledownloader $(ARGS)

fmt: ## Format all Go code
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (installed in the CI image)
	golangci-lint run

test: ## Run tests with race detector and coverage
	go test -race -cover ./...

cover: ## Open HTML coverage report
	go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out

ci: fmt-check vet lint test ## All quality gates

fmt-check:
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))" || (echo "gofmt required:"; gofmt -l $$(find . -name '*.go' -not -path './vendor/*'); exit 1)

docker-build: ## Build the runtime image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):latest .

docker-run: ## Run the image (args via ARGS="--help")
	docker run --rm -v "$$PWD/videos:/data" $(IMAGE):latest $(ARGS)

docker-test: ## Run quality gates inside the build container
	docker run --rm -v "$$PWD":/src -w /src golang:1.25-bookworm \
		sh -c "apt-get update >/dev/null 2>&1 && apt-get install -y ca-certificates ffmpeg chromium >/dev/null 2>&1; curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/local/bin; make ci"

clean:
	rm -rf bin coverage.out
