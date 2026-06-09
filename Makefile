PROTOC        := protoc
PROTOC_GEN_GO := $(shell which protoc-gen-go)

PROTO_DIR    := internal/proto
PROTO_FILES  := $(shell find $(PROTO_DIR) -name '*.proto')
BINARY       := edgegrid
IMAGE        := edgegrid:latest
COMPOSE_FILE := docker-compose/docker-compose.yml

.PHONY: all proto clean build run test docker-build compose-config compose-up compose-down compose-logs compose-ps run-compose

all: proto test build

proto:
ifndef PROTOC_GEN_GO
	$(error "protoc-gen-go not found. Please install with: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest")
endif
	@echo "Generating Go code from proto files..."
	@for file in $(PROTO_FILES); do \
		$(PROTOC) -I=internal/proto \
			--go_out=paths=source_relative:internal/proto \
			$$file || exit 1; \
	done

clean:
	@echo "Cleaning generated files..."
	@find $(PROTO_DIR) -name "*.pb.go" -type f -delete
	@rm -f $(BINARY)

build:
	go build -o $(BINARY) ./cmd/edgegrid

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
