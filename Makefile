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

.PHONY: hub
hub: ## Build the hub (RFC-005) into bin/
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BIN_DIR)/kergui-hub ./cmd/kergui-hub

HUB_TEST_DB ?= postgres://hub:hub@127.0.0.1:55432/hub?sslmode=disable

.PHONY: hub-db
hub-db: ## Start a throwaway Postgres for the hub tests (docker, port 55432)
	@docker inspect kergui-hub-pg >/dev/null 2>&1 || docker run -d --rm --name kergui-hub-pg \
		-e POSTGRES_USER=hub -e POSTGRES_PASSWORD=hub -e POSTGRES_DB=hub \
		-p 127.0.0.1:55432:5432 postgres:16-alpine >/dev/null
	@until docker exec kergui-hub-pg pg_isready -U hub -d hub >/dev/null 2>&1; do sleep 1; done

.PHONY: hub-test
hub-test: hub-db ## Run the hub tests, Postgres integration included
	KERGUI_HUB_TEST_DATABASE_URL='$(HUB_TEST_DB)' go test -count=1 ./cmd/kergui-hub ./internal/hub/...

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
serve: build ## Build and start the web dashboard + REST API on :8080 (reads .env if present)
	@echo ">> Kergui Fibre Manager — http://localhost:8080"
	@set -a; if [ -f .env ]; then echo ">> loading .env"; . ./.env; fi; set +a; \
	if [ -n "$${KERGUI_CAPTIVE}" ]; then echo ">> captive portal on: granting ports 53/80 (sudo)"; \
	  sudo setcap 'cap_net_bind_service=+ep' $(BIN_DIR)/$(BINARY) || exit 1; fi; \
	KERGUI_MASTER_KEY=$${KERGUI_MASTER_KEY:-dev-test-key} \
	$(BIN_DIR)/$(BINARY) serve \
		--router $${KERGUI_ROUTER_URL:-https://192.168.1.1} \
		--adapter $${KERGUI_ADAPTER:-zte_f6600p} \
		--router-insecure \
		--addr 127.0.0.1:8080 \
		$${KERGUI_CAPTIVE:+--captive}

.PHONY: captive-caps
captive-caps: build ## Let bin/kergui bind DNS :53 and portal :80 without root (asks for sudo; redo after each build)
	sudo setcap 'cap_net_bind_service=+ep' $(BIN_DIR)/$(BINARY)
	@getcap $(BIN_DIR)/$(BINARY)

.PHONY: docker-build
docker-build: ## Build the kergui Docker image
	docker compose build

.PHONY: up
up: ## Run kergui in Docker, always on (restarts at boot); state in ./data
	@mkdir -p data
	docker compose up -d --build

.PHONY: down
down: ## Stop the kergui container
	docker compose down

.PHONY: restart
restart: ## Restart the kergui container
	docker compose restart kergui

.PHONY: logs
logs: ## Follow the kergui container logs
	docker compose logs -f kergui

## --- Mobile core (gomobile; see docs/rfcs/RFC-004-standalone-mobile-app.md) ---

MOBILE_MODULE  := mobile/modules/kergui-core
GOMOBILE_BIN   := $(CURDIR)/$(BIN_DIR)/gomobile-tools
MOBILE_LDFLAGS := -s -w -X github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/mobilecore.version=$(or $(MOBILE_VERSION),dev)
ANDROID_API    ?= 24

.PHONY: gomobile-tools
gomobile-tools: ## Install gomobile + gobind (versions pinned by the tool block of go.mod)
	@mkdir -p $(GOMOBILE_BIN)
	GOBIN=$(GOMOBILE_BIN) go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind

.PHONY: mobile-core-android
mobile-core-android: gomobile-tools ## Build the Go core as an Android .aar (needs ANDROID_HOME + NDK)
	@mkdir -p $(MOBILE_MODULE)/android/libs
	PATH=$(GOMOBILE_BIN):$$PATH gomobile bind -target=android -androidapi $(ANDROID_API) \
		-javapkg org.kergui -ldflags='$(MOBILE_LDFLAGS)' \
		-o $(MOBILE_MODULE)/android/libs/kergui-core.aar ./mobilecore

.PHONY: mobile-core-ios
mobile-core-ios: gomobile-tools ## Build the Go core as an iOS .xcframework (macOS + Xcode only)
	@mkdir -p $(MOBILE_MODULE)/ios
	PATH=$(GOMOBILE_BIN):$$PATH gomobile bind -target=ios,iossimulator -iosversion 16.4 \
		-ldflags='$(MOBILE_LDFLAGS)' \
		-o $(MOBILE_MODULE)/ios/KerguiCore.xcframework ./mobilecore

.PHONY: clean
clean: ## Remove build artifacts and local demo state
	rm -rf $(BIN_DIR) coverage.out .mock.pid kergui.db
