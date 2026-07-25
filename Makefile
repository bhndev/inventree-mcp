BIN     := $(HOME)/.local/bin/inventree-mcp
VERSION := $(shell git describe --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" ./...

install:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/inventree-mcp
	@echo "installed $(BIN) ($(VERSION)) — quit and relaunch Claude Desktop"

test:
	go test ./...

docker:
	docker build --build-arg VERSION=$(VERSION) -t inventree-mcp:$(VERSION) -t inventree-mcp:local .

.PHONY: build install test docker
