.PHONY: build test clean install lint ci run fetch-deps bench

VERSION ?= dev
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -ldflags="-s -w -X 'github.com/luuuc/sense/internal/version.Version=$(VERSION)'"

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
	@command -v golangci-lint >/dev/null 2>&1 || \
		(echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	@PATH="$$PATH:$$(go env GOPATH)/bin" golangci-lint run

ci: build test lint
	@echo "All CI checks passed!"

bench:
	go test -bench=. -count=1 -run=^$$ ./internal/scan/ ./internal/blast/ ./internal/embed/ ./internal/search/

run:
	go run ./cmd/sense $(ARGS)
