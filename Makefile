BINARY  := jabledownloader
VERSION ?= 4.2.0
IMAGE   := jooservices/go-jabledownloader

GOFLAGS := CGO_ENABLED=0

.PHONY: build run fmt vet lint test cover cover-html ci docker-build docker-run docker-test clean

build: ## Build the CLI binary
	$(GOFLAGS) go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/jabledownloader

run: ## Run the CLI (args via ARGS="get jur-001")
	go run ./cmd/jabledownloader $(ARGS)

fmt: ## Format all Go code
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*' -not -path './.cache/*')

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
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*' -not -path './.cache/*'))" || (echo "gofmt required:"; gofmt -l $$(find . -name '*.go' -not -path './vendor/*' -not -path './.cache/*'); exit 1)

docker-build: ## Build the runtime image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):latest .

docker-run: ## Run the image (args via ARGS="--help")
	docker run --rm -it --shm-size=2g -v "$$PWD/videos:/data" -e CHROME_PATH=/usr/bin/chromium $(IMAGE):latest $(ARGS)

docker-test: ## Run quality gates inside the build container
	tools/ci/docker-compose build go
	tools/ci/docker-compose run --rm go make ci

release: ## Cross-compile release archives into dist/
	@mkdir -p dist && rm -f dist/*.tar.gz dist/checksums.txt
	tools/ci/docker-compose run --rm go sh -c '\
		set -e; mkdir -p /tmp/rel; \
		for os in darwin linux windows; do \
			for arch in amd64 arm64; do \
				name=jabledownloader_v$(VERSION)_$${os}_$${arch}; \
				ext=""; [ "$${os}" = windows ] && ext=".exe"; \
				rm -f /tmp/rel/jabledownloader /tmp/rel/jabledownloader.exe; \
				GOOS=$${os} GOARCH=$${arch} CGO_ENABLED=0 go build -trimpath \
					-ldflags "-s -w -X main.version=$(VERSION)" \
					-o /tmp/rel/jabledownloader$${ext} ./cmd/jabledownloader; \
				tar -C /tmp/rel -czf dist/$${name}.tar.gz jabledownloader$${ext}; \
			done; \
		done'
	shasum -a 256 dist/*.tar.gz > dist/checksums.txt
	@ls -lh dist/*.tar.gz

clean:
	rm -rf bin coverage.out
