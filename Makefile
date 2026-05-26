# common variables

SHA              := $(shell git describe --match=none --always --abbrev=8 --dirty)
TAG              ?= $(shell git describe --tag --always --dirty --match v[0-9]\*)
ABBREV_TAG       ?= $(shell git describe --tags >/dev/null 2>/dev/null && git describe --tag --always --match v[0-9]\* --abbrev=0 || echo 'undefined')
BRANCH           := $(shell git rev-parse --abbrev-ref HEAD)
ARTIFACTS        := _out
IMAGE_TAG        ?= $(TAG)
REGISTRY         ?= ghcr.io
USERNAME         ?= ktijssen
REGISTRY_AND_USERNAME ?= $(REGISTRY)/$(USERNAME)
TOOLCHAIN        ?= docker.io/golang:1.26-alpine

# tool versions baked into the Dockerfile
GOLANGCILINT_VERSION ?= v2.12.2
GOFUMPT_VERSION      ?= v0.10.0
GOVULNCHECK_VERSION  ?= latest
PROTOBUF_GO_VERSION  ?= latest

# go build flags
GO_BUILDFLAGS ?= -trimpath
GO_LDFLAGS    ?= -s -w
CGO_ENABLED   ?= 0
TESTPKGS      ?= ./...

# docker buildx settings
BUILD     := docker buildx build
PLATFORM  ?= linux/amd64
PROGRESS  ?= auto
PUSH      ?= false
CI_ARGS   ?=

COMMON_ARGS  = --file=Dockerfile
COMMON_ARGS += --provenance=false
COMMON_ARGS += --progress=$(PROGRESS)
COMMON_ARGS += --platform=$(PLATFORM)
COMMON_ARGS += --push=$(PUSH)
COMMON_ARGS += --build-arg=ARTIFACTS="$(ARTIFACTS)"
COMMON_ARGS += --build-arg=SHA="$(SHA)"
COMMON_ARGS += --build-arg=TAG="$(TAG)"
COMMON_ARGS += --build-arg=ABBREV_TAG="$(ABBREV_TAG)"
COMMON_ARGS += --build-arg=TOOLCHAIN="$(TOOLCHAIN)"
COMMON_ARGS += --build-arg=CGO_ENABLED="$(CGO_ENABLED)"
COMMON_ARGS += --build-arg=GO_BUILDFLAGS="$(GO_BUILDFLAGS)"
COMMON_ARGS += --build-arg=GO_LDFLAGS="$(GO_LDFLAGS)"
COMMON_ARGS += --build-arg=GOLANGCILINT_VERSION="$(GOLANGCILINT_VERSION)"
COMMON_ARGS += --build-arg=GOFUMPT_VERSION="$(GOFUMPT_VERSION)"
COMMON_ARGS += --build-arg=GOVULNCHECK_VERSION="$(GOVULNCHECK_VERSION)"
COMMON_ARGS += --build-arg=PROTOBUF_GO_VERSION="$(PROTOBUF_GO_VERSION)"
COMMON_ARGS += --build-arg=TESTPKGS="$(TESTPKGS)"

# default goal

.PHONY: all
all: unit-tests omni-infra-provider-ovhcloud image-omni-infra-provider-ovhcloud lint  ## Runs lint, unit-tests, builds binaries and image.

# generic build dispatchers

$(ARTIFACTS):
	@mkdir -p $(ARTIFACTS)

target-%:  ## Builds the specified Dockerfile target. Result stays in the build cache.
	@$(BUILD) --target=$* $(COMMON_ARGS) $(TARGET_ARGS) $(CI_ARGS) .

registry-%:  ## Builds the specified Dockerfile target and outputs an image. Pushes to the registry if PUSH=true.
	@$(MAKE) target-$* TARGET_ARGS="--tag=$(REGISTRY_AND_USERNAME)/$(IMAGE_NAME):$(IMAGE_TAG)"

local-%:  ## Builds the specified Dockerfile target and outputs to DEST locally.
	@$(MAKE) target-$* TARGET_ARGS="--output=type=local,dest=$(DEST) $(TARGET_ARGS)"
	@PLATFORM=$(PLATFORM) DEST=$(DEST) bash -c '\
	  for platform in $$(tr "," "\n" <<< "$$PLATFORM"); do \
	    directory="$${platform//\//_}"; \
	    if [[ -d "$$DEST/$$directory" ]]; then \
	      mv -f "$$DEST/$$directory/"* $$DEST/; \
	      rmdir "$$DEST/$$directory/"; \
	    fi; \
	  done'

# generated code

.PHONY: generate
generate:  ## Generate .proto definitions.
	@$(MAKE) local-$@ DEST=./

.PHONY: check-dirty
check-dirty: generate  ## Fails if the working tree has uncommitted changes after generate.
	@if test -n "`git status --porcelain`"; then echo "Source tree is dirty"; git status; git diff; exit 1 ; fi

# linting

.PHONY: lint-golangci-lint
lint-golangci-lint:  ## Runs golangci-lint.
	@$(MAKE) target-$@

.PHONY: lint-gofumpt
lint-gofumpt:  ## Runs gofumpt (checks formatting).
	@$(MAKE) target-$@

.PHONY: lint-govulncheck
lint-govulncheck:  ## Runs govulncheck.
	@$(MAKE) target-$@

.PHONY: lint
lint: lint-golangci-lint lint-gofumpt lint-govulncheck  ## Runs all linters.

# formatting

.PHONY: fmt
fmt:  ## Formats the source code with gofumpt.
	@docker run --rm -v $(PWD):/src -w /src $(TOOLCHAIN) \
		sh -c "go install mvdan.cc/gofumpt@$(GOFUMPT_VERSION) && gofumpt -w ."

# tests

.PHONY: unit-tests
unit-tests:  ## Runs unit tests and writes coverage.txt to _out/.
	@$(MAKE) local-$@ DEST=$(ARTIFACTS)

.PHONY: unit-tests-race
unit-tests-race:  ## Runs unit tests with race detection.
	@$(MAKE) target-$@

# binaries

.PHONY: $(ARTIFACTS)/omni-infra-provider-ovhcloud-linux-amd64
$(ARTIFACTS)/omni-infra-provider-ovhcloud-linux-amd64:
	@$(MAKE) local-omni-infra-provider-ovhcloud-linux-amd64 DEST=$(ARTIFACTS) PLATFORM=linux/amd64

.PHONY: omni-infra-provider-ovhcloud-linux-amd64
omni-infra-provider-ovhcloud-linux-amd64: $(ARTIFACTS)/omni-infra-provider-ovhcloud-linux-amd64  ## Builds the linux/amd64 binary.

.PHONY: $(ARTIFACTS)/omni-infra-provider-ovhcloud-linux-arm64
$(ARTIFACTS)/omni-infra-provider-ovhcloud-linux-arm64:
	@$(MAKE) local-omni-infra-provider-ovhcloud-linux-arm64 DEST=$(ARTIFACTS) PLATFORM=linux/arm64

.PHONY: omni-infra-provider-ovhcloud-linux-arm64
omni-infra-provider-ovhcloud-linux-arm64: $(ARTIFACTS)/omni-infra-provider-ovhcloud-linux-arm64  ## Builds the linux/arm64 binary.

.PHONY: omni-infra-provider-ovhcloud
omni-infra-provider-ovhcloud: omni-infra-provider-ovhcloud-linux-amd64 omni-infra-provider-ovhcloud-linux-arm64  ## Builds binaries for all supported platforms.

# image

.PHONY: image-omni-infra-provider-ovhcloud
image-omni-infra-provider-ovhcloud:  ## Builds the runtime container image. Use PUSH=true to push.
	@$(MAKE) registry-image-omni-infra-provider-ovhcloud IMAGE_NAME="omni-infra-provider-ovhcloud" PLATFORM="linux/amd64,linux/arm64"

# compose helpers (kept from original Makefile)

.PHONY: up
up:  ## docker compose up (local dev).
	docker compose build
	docker compose up --force-recreate -d

.PHONY: down
down:  ## docker compose down (local dev).
	docker compose down --remove-orphans

.PHONY: logs
logs:  ## Follow docker compose logs.
	docker compose logs -f

# misc

.PHONY: tidy
tidy:  ## Runs go mod tidy in a Docker toolchain container.
	@docker run --rm -v $(PWD):/src -w /src $(TOOLCHAIN) \
		sh -c "go mod tidy"

.PHONY: clean
clean:  ## Removes the artifacts directory.
	@rm -rf $(ARTIFACTS)

.PHONY: renovate-local
renovate-local:  ## Runs renovate locally to check syntax and test configuration.
	@docker run --rm \
		--user $(shell id -u):$(shell id -g) \
		-v $(PWD):/src \
		-w /src \
		-e GITHUB_TOKEN \
		-e LOG_LEVEL=debug \
		-e RENOVATE_PLATFORM=local \
		-e RENOVATE_DRY_RUN=full \
		renovate/renovate

.PHONY: help
help:  ## Shows this help.
	@grep -E '^[a-zA-Z%_/$$()-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-32s\033[0m %s\n", $$1, $$2}'
