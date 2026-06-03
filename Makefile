.PHONY: build test clean install lint fmt ci run fetch-deps bench smoke

VERSION ?= dev
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -ldflags="-s -w -X 'github.com/luuuc/sense/internal/version.Version=$(VERSION)'"

# Pinned so the gate definition (v2 formatters: block, complexity thresholds)
# does not float with upstream releases. Keep in sync with .github/workflows/ci.yml.
GOLANGCI_VERSION ?= v2.12.2
ensure-golangci = command -v golangci-lint >/dev/null 2>&1 || \
	(echo "Installing golangci-lint $(GOLANGCI_VERSION)..." && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION))

fetch-deps: ## Downloads model + ORT lib on first run; no-ops if already present
	./scripts/fetch-deps.sh --local

build: fetch-deps
	go build $(LDFLAGS) -trimpath -o bin/sense ./cmd/sense

test:
	go test -v ./...

clean:
	rm -rf bin/ dist/

install: build
	cp bin/sense /usr/local/bin/sense

lint:
	@$(ensure-golangci)
	@PATH="$$PATH:$$(go env GOPATH)/bin" golangci-lint run

fmt:
	@$(ensure-golangci)
	@PATH="$$PATH:$$(go env GOPATH)/bin" golangci-lint fmt

ci: build test lint
	@echo "All CI checks passed!"

smoke:
	go test -v -run TestSmoke ./internal/smoke/

bench:
	go test -bench=. -count=1 -run=^$$ ./internal/scan/ ./internal/blast/ ./internal/embed/ ./internal/search/

run:
	go run ./cmd/sense $(ARGS)
