PROTOC            := protoc
PROTOC_GEN_GO     := $(shell which protoc-gen-go)

PROTO_DIR         := internal/proto
PROTO_FILES       := $(shell find $(PROTO_DIR) -name '*.proto')

.PHONY: all clean proto build run docker-build run-compose

all: proto

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




# Building local binary
build:
	GOTOOLCHAIN=local go build -o edgegrid ./cmd/edgegrid

# Running local binary
run: build
	./edgegrid

# Building docker images
docker-build:
	docker build -t edgegrid:latest .


# Running docker compose
run-compose:
	cd docker-compose && docker compose up

