# Demo runners for the beats input plugin. Run from this directory.
# Build/test the plugin itself with the go commands in README.md — they need a
# go.mod fixup step that doesn't belong behind a Make target.
.DEFAULT_GOAL := help
.PHONY: help demo demo-tls test test-integration test-transport down clean

CERTS := example/tls/certs/server.crt

help: ## Show this help
	@awk 'BEGIN{FS=":.*##"} /^[a-zA-Z_-]+:.*##/{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

demo: ## Plaintext end-to-end demo (flog -> filebeat -> plugin -> stdout)
	docker compose -f example/docker-compose.yml up --build

demo-tls: $(CERTS) ## mTLS end-to-end demo (generates certs on first run)
	docker compose -f example/docker-compose.tls.yml up --build

$(CERTS):
	example/tls/gen-certs.sh

test: ## Unit tests (no Docker required)
	CGO_ENABLED=1 go test -v ./...

test-integration: ## Integration tests: Filebeat 6, 7, 8 — version matrix (requires Docker)
	CGO_ENABLED=1 go test -v -tags integration -run 'TestFilebeat' -timeout 10m ./...

test-transport: ## Integration tests: no-TLS, TLS, mTLS transport matrix (requires Docker)
	CGO_ENABLED=1 go test -v -tags integration -run 'TestTransport' -timeout 10m ./...

down: ## Stop both demo stacks and drop their volumes
	-docker compose -f example/docker-compose.yml down -v
	-docker compose -f example/docker-compose.tls.yml down -v

clean: down ## down + remove the generated certs
	rm -rf example/tls/certs
