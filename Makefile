BINARY       := edgegrid
IMAGE        := edgegrid:latest
COMPOSE_FILE := docker-compose/docker-compose.yml
DIST_DIR     := dist

# Populated from git tag if available, otherwise "dev"
VERSION      := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS      := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all clean build build-all run test docker-build compose-config compose-up compose-down compose-logs compose-ps run-compose

all: test build

clean:
	@rm -f $(BINARY)
	@rm -rf $(DIST_DIR)

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/edgegrid

# Cross-compile for every platform workers are likely to run on.
# Output: dist/edgegrid-<os>-<arch>[.exe]
build-all:
	@mkdir -p $(DIST_DIR)
	GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o $(DIST_DIR)/edgegrid-linux-amd64   ./cmd/edgegrid
	GOOS=linux   GOARCH=arm64  go build $(LDFLAGS) -o $(DIST_DIR)/edgegrid-linux-arm64   ./cmd/edgegrid
	GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o $(DIST_DIR)/edgegrid-darwin-amd64  ./cmd/edgegrid
	GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o $(DIST_DIR)/edgegrid-darwin-arm64  ./cmd/edgegrid
	GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o $(DIST_DIR)/edgegrid-windows-amd64.exe ./cmd/edgegrid
	@echo "Built $(VERSION) → $(DIST_DIR)/"
	@ls -lh $(DIST_DIR)/

run: build
	./$(BINARY)

test:
	go test ./...

docker-build:
	docker build -t $(IMAGE) .

compose-config:
	docker compose -f $(COMPOSE_FILE) config

compose-up:
	docker compose -f $(COMPOSE_FILE) up --build

compose-down:
	docker compose -f $(COMPOSE_FILE) down

compose-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

compose-ps:
	docker compose -f $(COMPOSE_FILE) ps

run-compose: compose-up
