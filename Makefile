## Kergui Fibre Manager — developer tasks
# Pure Go toolchain; no cgo. Run `make help` for the list.

BINARY      := kergui
PKG         := ./...
BIN_DIR     := bin
MOCK_PORT   ?= 18080
GOFLAGS     ?=

.DEFAULT_GOAL := help

.PHONY: help
help: ## List available targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the kergui CLI into bin/
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BIN_DIR)/$(BINARY) ./cmd/kergui

.PHONY: mock
mock: ## Build the standalone mock ZTE router (for demos / manual testing)
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BIN_DIR)/mockrouter ./cmd/mockrouter

.PHONY: test
test: ## Run all tests
	go test $(PKG)

.PHONY: cover
cover: ## Run tests with coverage summary
	go test -coverprofile=coverage.out $(PKG) && go tool cover -func=coverage.out | tail -n 1

.PHONY: vet
vet: ## Run go vet
	go vet $(PKG)

.PHONY: fmt
fmt: ## Format the code
	gofmt -w $(shell find . -name '*.go' -not -path './vendor/*')

.PHONY: fmt-check
fmt-check: ## Fail if code is not gofmt-clean
	@out=$$(gofmt -l $(shell find . -name '*.go' -not -path './vendor/*')); \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: check
check: fmt-check vet test ## fmt-check + vet + test

.PHONY: demo
demo: build mock ## Boot the mock ZTE router and run the read-only CLI flow against it
	@echo ">> starting mock ZTE F660 on :$(MOCK_PORT)"
	@$(BIN_DIR)/mockrouter -addr 127.0.0.1:$(MOCK_PORT) & echo $$! > .mock.pid; \
	sleep 1; \
	R=http://127.0.0.1:$(MOCK_PORT); \
	echo "== discover =="      ; $(BIN_DIR)/$(BINARY) discover --router $$R ; \
	echo "== login --test =="  ; KERGUI_MASTER_KEY=demo-pass $(BIN_DIR)/$(BINARY) login --test --router $$R --username admin --password admin ; \
	echo "== devices =="       ; KERGUI_MASTER_KEY=demo-pass $(BIN_DIR)/$(BINARY) devices --router $$R --username admin --password admin ; \
	echo "== inspect =="       ; KERGUI_MASTER_KEY=demo-pass $(BIN_DIR)/$(BINARY) inspect  --router $$R --username admin --password admin ; \
	kill $$(cat .mock.pid) 2>/dev/null; rm -f .mock.pid

.PHONY: serve
serve: build ## Build and start the web dashboard + REST API on :8080
	@echo ">> Kergui Fibre Manager — http://localhost:8080"
	KERGUI_MASTER_KEY=$${KERGUI_MASTER_KEY:-dev-test-key} \
	$(BIN_DIR)/$(BINARY) serve \
		--router $${KERGUI_ROUTER_URL:-https://192.168.1.1} \
		--adapter $${KERGUI_ADAPTER:-zte_f6600p} \
		--router-insecure \
		--addr 127.0.0.1:8080

.PHONY: clean
clean: ## Remove build artifacts and local demo state
	rm -rf $(BIN_DIR) coverage.out .mock.pid kergui.db
