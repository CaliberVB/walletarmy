SHELL := /usr/bin/env bash
.SHELLFLAGS = -euo pipefail -c

# ============================================================================
# Variables
# ============================================================================
GO := go
TEST_PKGS := $(shell go list ./... | grep -v /vendor/)

# ============================================================================
# Default Target
# ============================================================================
.DEFAULT_GOAL := help

# ============================================================================
# Main Targets
# ============================================================================
.PHONY: build
build: ## Build the project
	@echo "Building..."
	@$(GO) build -v ./...
	@echo "✅ Build complete!"

.PHONY: test
test: ## Run all tests
	@echo "Running tests..."
	@$(GO) test $(TEST_PKGS) -timeout 600s -count=1
	@echo "✅ Tests complete!"

.PHONY: test-verbose
test-verbose: ## Run tests with verbose output
	@echo "Running tests (verbose)..."
	@$(GO) test -v $(TEST_PKGS) -timeout 600s -count=1

.PHONY: test-coverage
test-coverage: ## Run tests with coverage report
	@echo "Running tests with coverage..."
	@$(GO) test $(TEST_PKGS) -timeout 600s -count=1 -coverprofile=coverage.out -covermode=atomic
	@$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report: coverage.html"
	@$(GO) tool cover -func=coverage.out | grep total | awk '{print "Total coverage: " $$3}'

.PHONY: format
format: ## Format all Go files
	@echo "🎨 Formatting Go files..."
	@find . -name "*.go" -type f -not -path "*/vendor/*" -exec gofmt -w {} \;
	@command -v goimports > /dev/null && goimports -w . || true
	@command -v gci > /dev/null && gci write --skip-generated -s standard -s default -s "prefix(github.com/tranvictor/walletarmy)" . || true
	@command -v golines > /dev/null && golines -w -m 120 . || true
	@command -v gofumpt > /dev/null && gofumpt -extra -w . || true
	@echo "✅ Formatting complete!"

.PHONY: format-check
format-check: ## Check if Go files are properly formatted
	@echo "🔍 Checking formatting..."
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "❌ Files not formatted:"; \
		gofmt -l . | sed 's/^/  /'; \
		exit 1; \
	else \
		echo "✅ All files properly formatted"; \
	fi

.PHONY: lint
lint: ## Run linter
	@echo "Running linters..."
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
		echo "✅ Linting complete!"; \
	else \
		echo "⚠️  golangci-lint not installed, running go vet..."; \
		$(GO) vet ./...; \
		echo "✅ Vet complete!"; \
	fi

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	@$(GO) vet ./...
	@echo "✅ Vet complete!"

# ============================================================================
# Utility Targets
# ============================================================================
.PHONY: clean
clean: ## Clean build artifacts and coverage files
	@echo "Cleaning..."
	@$(GO) clean
	@rm -f coverage.out coverage.html
	@echo "✅ Clean complete!"

.PHONY: tidy
tidy: ## Tidy go modules
	@echo "Tidying modules..."
	@$(GO) mod tidy
	@echo "✅ Modules tidied!"

.PHONY: deps
deps: ## Download dependencies
	@echo "Downloading dependencies..."
	@$(GO) mod download
	@echo "✅ Dependencies downloaded!"

# ============================================================================
# Composite Targets
# ============================================================================
.PHONY: pre-commit
pre-commit: format vet test ## Run pre-commit checks
	@echo "✅ Pre-commit checks passed!"

.PHONY: check
check: format-check vet test ## Run all checks
	@echo "✅ All checks passed!"

.PHONY: ci
ci: deps format-check vet test-coverage ## Run CI pipeline
	@echo "✅ CI pipeline complete!"

# ============================================================================
# Help Target
# ============================================================================
.PHONY: help
help: ## Show this help message
	@echo "Walletarmy - Available targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Examples:"
	@echo "  make test          # Run all tests"
	@echo "  make build         # Build the project"
	@echo "  make format        # Format code"
	@echo "  make lint          # Run linter"
	@echo "  make pre-commit    # Run pre-commit checks"
	@echo ""
