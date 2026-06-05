.PHONY: build test test-hermetic cover ledger clean install lint fmt ci run fetch-deps bench smoke

VERSION ?= dev
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -ldflags="-s -w -X 'github.com/luuuc/sense/internal/version.Version=$(VERSION)'"

# Single source of the golangci-lint version: CI runs `make lint`, so it
# inherits this pin. Pinned (not "latest") so the gate definition — the v2
# formatters: block and the gocyclo/gocognit thresholds — does not float with
# upstream releases.
GOLANGCI_VERSION ?= v2.12.2
ensure-golangci = command -v golangci-lint >/dev/null 2>&1 || \
	(echo "Installing golangci-lint $(GOLANGCI_VERSION)..." && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION))

fetch-deps: ## Downloads model + ORT lib on first run; no-ops if already present
	./scripts/fetch-deps.sh --local

build: fetch-deps
	go build $(LDFLAGS) -trimpath -o bin/sense ./cmd/sense

test:
	go test -v ./...

# Goal 5 (no side effects), enforced by outcome: these packages must pass
# with no network, no ONNX, and no external binary. The set RATCHETS — it
# grows per-package as the testability arc (27-02→04) injects seams, fully
# on by 27-04. A package only joins once it can be unit-tested offline.
# CI runs this under an unprivileged network namespace (the network bite);
# the set guarantees no ONNX/external-binary by construction (no embed/exec deps).
#
# This set currently equals the depguard `pure-core` list in .golangci.yml on
# purpose — those packages are both effect-free (depguard) and offline-testable
# (here). Grow the two together; they will diverge once a package that is
# offline-testable but not import-clean joins the hermetic set.
HERMETIC_PKGS := \
	./internal/extract/... \
	./internal/blast/... \
	./internal/conventions/... \
	./internal/mcpio/... \
	./internal/model/...

test-hermetic:
	go test $(HERMETIC_PKGS)

# Coverage gate. Two floors over one profile:
#   1. Per-file (PRIMARY) — scripts/coveragegate asserts every production file in
#      the whole tree holds >= 92% line AND function coverage. Deny-by-default:
#      gated unless a _test.go file, a justified straggler-exception, or in a
#      pinned excludedDir (test-support packages + the gate's own tooling). Its
#      excludedDir / straggler-exception lists are config-as-code in that package,
#      with unit tests that red a synthetic sub-floor file and prove a brand-new
#      package is gated automatically. Code in a new package holds the floor with
#      no allow-list edit (28-02 inverted the cycle-27 allow-list).
#   2. Total-% (BACKSTOP) — a coarse gross-regression check; the suite measured
#      94.0% on 2026-06-04, so the floor sits below it with headroom for
#      run-to-run and cross-platform variance.
#
# The hard fail is these local checks, NOT a Codecov status, so a flaky upload
# never reds the build.
#
# The onnx_integration tag is set here (and ONLY here) so the gate exercises the
# CGO embedding shell (internal/embed/onnx.go) for real — the one place the
# bundled model and ORT runtime run inference. It depends on fetch-deps so the
# integration test always has its deps and fails loud (never skips) if they are
# somehow absent. The plain `go test ./...` / `make test` / `make test-hermetic`
# paths carry NO tag, so unit tests stay ONNX-free; the tag lives only in the
# coverage gate, which already requires the deps `make build` fetches.
COVER_FLOOR ?= 92
cover: fetch-deps
	go test -race -count=1 -tags onnx_integration -coverprofile=coverage.txt -coverpkg=./... ./...
	go run ./scripts/coveragegate -profile=coverage.txt
	@# Backstop. Parse depends on `go tool cover`'s total line being last and
	@# %-suffixed. If that format ever changes, t becomes 0 and the gate fails
	@# closed (safe).
	@total=$$(go tool cover -func=coverage.txt | awk 'END {gsub(/%/,"",$$NF); print $$NF}'); \
	awk -v t="$$total" -v f="$(COVER_FLOOR)" 'BEGIN { \
		printf "total coverage: %s%% (floor: %s%%)\n", t, f; \
		if (t+0 < f+0) { printf "FAIL: coverage %s%% is below the %s%% floor\n", t, f; exit 1 } \
	}'

# Complexity-ledger burndown — now at its terminal value, ZERO. Every inline
# //nolint:gocyclo/gocognit was tracked debt a 27-05→13 split/decompose pitch
# retired; the cycle's mechanical exit condition is `grep -rc 'nolint:goc' == 0`.
# 27-13 retired the last two survivors (27-07's resolver.go entries) by splitting
# Resolve into resolveQualified/resolveByLeaf and isTestPath into a table-driven
# form. A new //nolint:gocyclo/gocognit now reds CI: decompose the function, do
# not suppress it. (Matches gocyclo/gocognit only — an unrelated gocritic
# suppression is not debt.)
LEDGER_MAX ?= 0
ledger:
	@n=$$(grep -rnE 'nolint:goc(yclo|ognit)' --include='*.go' internal cmd | wc -l | tr -d ' '); \
	echo "complexity ledger: $$n entries (cap: $(LEDGER_MAX))"; \
	if [ "$$n" -gt "$(LEDGER_MAX)" ]; then \
		echo "FAIL: ledger grew past $(LEDGER_MAX). Decompose the function or retire an entry — do not add a new //nolint."; \
		exit 1; \
	fi

clean:
	rm -rf bin/ dist/
	rm -f coverage.txt

install: build
	cp bin/sense /usr/local/bin/sense

lint:
	@$(ensure-golangci)
	@PATH="$$PATH:$$(go env GOPATH)/bin" golangci-lint run

fmt:
	@$(ensure-golangci)
	@PATH="$$PATH:$$(go env GOPATH)/bin" golangci-lint fmt

ci: build cover lint ledger
	@echo "All CI checks passed!"

smoke:
	go test -v -run TestSmoke ./internal/smoke/

bench:
	go test -bench=. -count=1 -run=^$$ ./internal/scan/ ./internal/blast/ ./internal/embed/ ./internal/search/

run:
	go run ./cmd/sense $(ARGS)
