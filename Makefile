# Paradox Mod Localization Extender — build automation.
#
# Default builds drop into ./bin/. Cross-compile targets assume the toolchains
# documented in README.md (mingw-w64 for Windows, osxcross for macOS). Override
# any of the *_CC variables or *_BIN paths via the environment if your install
# differs.

.DEFAULT_GOAL := help

# --- Configuration ---------------------------------------------------------

GO       ?= go
BIN_DIR  ?= bin
BINARY   ?= paradox-localize
VERSION  := $(shell grep -E '^const Version' internal/config/config.go | sed -E 's/.*"([^"]+)".*/\1/')
LDFLAGS  := -s -w

# Cross-compile toolchains. Override these in your environment if needed.
OSXCROSS_BIN ?= $(HOME)/osxcross/target/bin
LLVM_BIN     ?= /usr/lib/llvm-20/bin
WIN_AMD64_CC ?= x86_64-w64-mingw32-gcc
MAC_AMD64_CC ?= o64-clang
MAC_ARM64_CC ?= aarch64-apple-darwin23.5-clang

# Output paths
LINUX_BIN     := $(BIN_DIR)/$(BINARY)-linux-amd64
WINDOWS_BIN   := $(BIN_DIR)/$(BINARY)-windows-amd64.exe
MAC_INTEL_BIN := $(BIN_DIR)/$(BINARY)-macos-amd64
MAC_ARM_BIN   := $(BIN_DIR)/$(BINARY)-macos-arm64

# --- Phony targets ---------------------------------------------------------

.PHONY: help run test vet fmt fmt-check tidy check clean version \
        build-linux build-windows build-mac-intel build-mac-arm build-all release

help: ## Show this help
	@printf "Paradox Localize %s — Makefile targets\n\n" "$(VERSION)"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# --- Local development -----------------------------------------------------

run: build-linux ## Build and run the GUI
	./$(LINUX_BIN)

test: ## Run unit tests
	$(GO) test ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format all Go files in place
	$(GO) fmt ./...

fmt-check: ## Fail if any file needs formatting (CI-friendly)
	@out="$$(gofmt -l . 2>&1)"; \
	if [ -n "$$out" ]; then \
		echo "Files need gofmt:"; echo "$$out"; exit 1; \
	fi

tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

check: fmt-check vet test ## Run formatter check, vet, and tests

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)

version: ## Print the project version
	@echo $(VERSION)

# --- Build matrix ----------------------------------------------------------

build-linux: $(LINUX_BIN) ## Build Linux x86-64 binary

build-windows: $(WINDOWS_BIN) ## Cross-compile Windows x86-64 binary (needs mingw-w64)

build-mac-intel: $(MAC_INTEL_BIN) ## Cross-compile macOS Intel binary (needs osxcross)

build-mac-arm: $(MAC_ARM_BIN) ## Cross-compile macOS Apple Silicon binary (needs osxcross)

build-all: build-linux build-windows build-mac-intel build-mac-arm ## Build all four platforms

release: clean build-all ## Clean rebuild of every platform (use for tagged releases)
	@echo
	@echo "Built artifacts:"
	@ls -lh $(BIN_DIR)/

# --- Recipes ---------------------------------------------------------------

# The binaries always go through `go build`: make cannot see the Go sources,
# and Go's own build cache makes an unchanged rebuild instant.
.PHONY: FORCE
FORCE:

$(LINUX_BIN): FORCE | $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $@ .

$(WINDOWS_BIN): FORCE | $(BIN_DIR)
	CC=$(WIN_AMD64_CC) CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
	    $(GO) build -ldflags "$(LDFLAGS)" -o $@ .

$(MAC_INTEL_BIN): FORCE | $(BIN_DIR)
	PATH="$(OSXCROSS_BIN):$(LLVM_BIN):$$PATH" \
	CC=$(MAC_AMD64_CC) CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
	    $(GO) build -ldflags "$(LDFLAGS)" -o $@ .

$(MAC_ARM_BIN): FORCE | $(BIN_DIR)
	PATH="$(OSXCROSS_BIN):$(LLVM_BIN):$$PATH" \
	CC=$(MAC_ARM64_CC) CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
	    $(GO) build -ldflags "$(LDFLAGS)" -o $@ .

$(BIN_DIR):
	mkdir -p $@
