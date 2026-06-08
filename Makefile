PROTOC            := protoc
PROTOC_GEN_GO     := $(shell which protoc-gen-go)

PROTO_DIR         := apps/shared/proto
PROTO_FILES       := $(shell find $(PROTO_DIR) -name '*.proto')

.PHONY: all clean proto docker-coordinator docker-worker run-compose

all: proto

proto:
ifndef PROTOC_GEN_GO
	$(error "protoc-gen-go not found. Please install with: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest")
endif
	@echo "Generating Go code from proto files..."
	@for file in $(PROTO_FILES); do \
		$(PROTOC) -I=apps/shared/proto \
			--go_out=paths=source_relative:apps/shared/proto \
			$$file || exit 1; \
	done

clean:
	@echo "Cleaning generated files..."
	@find $(PROTO_DIR) -name "*.pb.go" -type f -delete




# Building docker images
docker-coordinator:
	docker build -t edgegrid/coordinator:latest -f ./apps/coordinator/Dockerfile ./apps

docker-worker:
	docker build -t edgegrid/worker:latest -f ./apps/worker/Dockerfile ./apps


# Running docker compose
run-compose:
	cd docker-compose && docker compose up

