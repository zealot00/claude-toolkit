BINARY  := claude-toolkit
MODULE  := github.com/zealot00/claude-toolkit
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/cmd.Version=$(VERSION) \
	-X $(MODULE)/cmd.Commit=$(COMMIT) \
	-X $(MODULE)/cmd.Date=$(DATE)

PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the binary into ./bin
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .
	@echo "built bin/$(BINARY) $(VERSION)"

.PHONY: install
install: ## Install into $$GOBIN (or $$GOPATH/bin)
	go install -trimpath -ldflags '$(LDFLAGS)' .

.PHONY: test
test: ## Run the test suite with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests and open the coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w .

.PHONY: lint
lint: vet ## Vet, check formatting, and shellcheck the installer
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi
	@command -v shellcheck >/dev/null 2>&1 && shellcheck scripts/install.sh || echo "shellcheck not installed, skipping"

.PHONY: check
check: lint test ## Everything CI runs

.PHONY: dist
dist: ## Cross-compile release archives into ./dist
	@rm -rf dist && mkdir -p dist
	@cp -R .claude-plugin dist/ 2>/dev/null || true
	@cp -R commands dist/ 2>/dev/null || true
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/$(BINARY)$$ext . || exit 1; \
		tar -czf dist/$(BINARY)_$${os}_$${arch}.tar.gz -C dist $(BINARY)$$ext .claude-plugin commands || exit 1; \
		rm -f dist/$(BINARY)$$ext; \
	done
	@rm -rf dist/.claude-plugin dist/commands
	@cd dist && shasum -a 256 *.tar.gz > checksums.txt
	@echo "archives in ./dist"

.PHONY: snapshot
snapshot: ## Dry-run the real release pipeline (requires goreleaser)
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist coverage.out

.PHONY: help
help: ## List available targets
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
