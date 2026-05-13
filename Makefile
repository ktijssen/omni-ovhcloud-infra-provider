PROJECT      := omni-infra-provider-ovhcloud
IMAGE        ?= $(PROJECT):dev
GO           ?= go
PROTOC       ?= protoc

PROTO_DIR    := api/specs
PROTO_FILE   := $(PROTO_DIR)/specs.proto
PB_OUT       := $(PROTO_DIR)/specs.pb.go

.PHONY: all
all: build

.PHONY: generate
generate:
	@command -v $(PROTOC) >/dev/null 2>&1 || { \
		echo >&2 "protoc not found. Try: nix-shell -p protobuf --run 'make generate'"; exit 1; }
	@command -v protoc-gen-go >/dev/null 2>&1 || $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	$(PROTOC) -I=$(PROTO_DIR) \
		--go_out=paths=source_relative:$(PROTO_DIR) \
		$(PROTO_FILE)

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -o _out/$(PROJECT) ./cmd/omni-infra-provider-ovhcloud

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: test
test:
	$(GO) test ./...

.PHONY: image
image:
	docker build -t $(IMAGE) .

.PHONY: up
up:
	docker compose build #--no-cache
	docker compose up --force-recreate -d

.PHONY: down
down:
	docker compose down --remove-orphans

.PHONY: logs
logs:
	docker compose logs -f

.PHONY: clean
clean:
	rm -rf _out
