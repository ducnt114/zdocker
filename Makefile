BINARY := bin/zdocker
PKG    := ./cmd/zdocker

# Demo containers carry this label so demo-clean removes only them.
DEMO_LABEL := zdocker.demo=1
FAKE_ADDR  := 127.0.0.1:2375

.PHONY: build test lint run run-fake demo demo-clean clean help

build: ## Build the zdocker binary
	go build -o $(BINARY) $(PKG)

test: ## Run unit tests
	go test ./...

lint: ## go vet, plus golangci-lint when installed
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed, skipping"; fi

run: build ## Run against your Docker daemon ($$DOCKER_HOST or the local socket)
	./$(BINARY)

run-fake: build ## Run against the built-in fake daemon (no Docker needed)
	@go run ./hack/fakedaemon -addr $(FAKE_ADDR) & echo $$! > .fakedaemon.pid; sleep 1
	-./$(BINARY) --host tcp://$(FAKE_ADDR)
	@kill `cat .fakedaemon.pid` 2>/dev/null; rm -f .fakedaemon.pid

demo: ## Start a few labelled sample containers on your Docker daemon
	docker run -d --label $(DEMO_LABEL) --name zdocker-chatty busybox:1.36 \
		sh -c 'i=0; while true; do echo "tick $$i"; echo "warn $$i" >&2; i=$$((i+1)); sleep 1; done'
	docker run -d --label $(DEMO_LABEL) --name zdocker-crashloop --restart on-failure:100 busybox:1.36 \
		sh -c 'echo starting; sleep 3; echo boom >&2; exit 1'
	docker run -d --label $(DEMO_LABEL) --name zdocker-web -p 8080:80 nginx:1.27
	docker run --label $(DEMO_LABEL) --name zdocker-done busybox:1.36 echo finished

demo-clean: ## Remove only the sample containers (by label)
	docker ps -aq --filter label=$(DEMO_LABEL) | xargs -r docker rm -f

clean: ## Remove build output
	rm -rf bin

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
