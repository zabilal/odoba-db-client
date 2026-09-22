BINARY   := ikigai
PKG      := ./...
CORE     := ./internal/source/... ./internal/model/... ./internal/diff/...
GOFLAGS  ?=

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
	  | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the desktop binary (requires CGO — see NFR-D3)
	CGO_ENABLED=1 go build $(GOFLAGS) -o $(BINARY) ./cmd/ikigai

.PHONY: test
test: ## Run unit tests
	go test $(GOFLAGS) -race $(PKG)

.PHONY: cover
cover: ## Core coverage report (NFR-Q2 target: >=70%)
	go test -coverprofile=coverage.out $(CORE)
	@go tool cover -func=coverage.out | tail -1

.PHONY: lint
lint: ## Run golangci-lint (includes the ARCH-1 depguard rule)
	golangci-lint run $(PKG)

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: bench
bench: ## Run benchmarks (NFR-Q3 budget assertions)
	go test -run '^$$' -bench . -benchmem $(PKG)

.PHONY: conformance
conformance: ## Run the driver conformance suite against real servers (REQ-DRV-1)
	go test -tags=conformance -timeout 20m ./internal/source/...

.PHONY: kafka-up
kafka-up: ## Start the Kafka brokers and schema registry the tagged tests read
	./scripts/kafka.sh up

.PHONY: kafka-down
kafka-down: ## Remove those containers and the network they share
	./scripts/kafka.sh down

.PHONY: package
package: ## Produce a platform installer via fyne package (NFR-D4)
	fyne package --release

.PHONY: tidy
tidy: ## Tidy module dependencies
	go mod tidy

.PHONY: check
check: vet lint test ## Everything CI runs on a pull request

.PHONY: clean
clean:
	rm -rf $(BINARY) dist build coverage.out
