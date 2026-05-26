# syntax = docker/dockerfile-upstream:1.24.0-labs

ARG TOOLCHAIN=docker.io/golang:1.26-alpine
ARG TARGETARCH
ARG GOLANGCILINT_VERSION=v2.12.2
ARG GOFUMPT_VERSION=v0.10.0
ARG GOVULNCHECK_VERSION=latest
ARG PROTOBUF_GO_VERSION=latest

FROM ghcr.io/siderolabs/ca-certificates:v1.12.0 AS image-ca-certificates

FROM ghcr.io/siderolabs/fhs:v1.12.0 AS image-fhs

# collects proto specs
FROM scratch AS proto-specs
ADD api/specs/specs.proto /api/specs/

# base toolchain image
FROM --platform=${BUILDPLATFORM} ${TOOLCHAIN} AS toolchain
RUN apk --update --no-cache add bash build-base curl jq protoc protobuf-dev

# build tools
FROM --platform=${BUILDPLATFORM} toolchain AS tools
ENV GO111MODULE=on
ENV CGO_ENABLED=0
ENV GOPATH=/go
ARG PROTOBUF_GO_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOBUF_GO_VERSION} \
 && mv /go/bin/protoc-gen-go /bin/protoc-gen-go
ARG GOLANGCILINT_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCILINT_VERSION} \
 && mv /go/bin/golangci-lint /bin/golangci-lint
ARG GOFUMPT_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    go install mvdan.cc/gofumpt@${GOFUMPT_VERSION} \
 && mv /go/bin/gofumpt /bin/gofumpt
ARG GOVULNCHECK_VERSION
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} \
 && mv /go/bin/govulncheck /bin/govulncheck

# tools and sources
FROM tools AS base
WORKDIR /src
COPY go.mod go.mod
COPY go.sum go.sum
RUN --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg go mod download
RUN --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg go mod verify
COPY ./api ./api
COPY ./cmd ./cmd
COPY ./internal ./internal
RUN --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg go list -mod=readonly all >/dev/null

# runs protobuf compiler
FROM tools AS proto-compile
COPY --from=proto-specs / /
RUN protoc -I/api/specs --go_out=paths=source_relative:/api/specs /api/specs/specs.proto
RUN rm /api/specs/specs.proto

# cleaned up specs and compiled versions
FROM scratch AS generate
COPY --from=proto-compile /api/ /api/

# checks formatting with gofumpt
FROM base AS lint-gofumpt
RUN FILES="$(gofumpt -l .)" && test -z "${FILES}" || (echo -e "Source code is not formatted with 'gofumpt -w .':\n${FILES}"; exit 1)

# runs golangci-lint
FROM base AS lint-golangci-lint
COPY .golangci.yml .golangci.yml
ENV GOGC=50
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/root/.cache/golangci-lint,id=omni-infra-provider-ovhcloud/root/.cache/golangci-lint,sharing=locked \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    golangci-lint run --config .golangci.yml

# runs govulncheck
FROM base AS lint-govulncheck
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    govulncheck ./...

# runs unit tests and emits coverage.txt
FROM base AS unit-tests-run
ARG TESTPKGS=./...
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    --mount=type=cache,target=/tmp,id=omni-infra-provider-ovhcloud/tmp \
    go test -covermode=atomic -coverprofile=/coverage.txt -coverpkg=${TESTPKGS} ${TESTPKGS}

# scratch stage that exports coverage.txt as the build output
FROM scratch AS unit-tests
COPY --from=unit-tests-run /coverage.txt /coverage.txt

# runs unit tests with race detection
FROM base AS unit-tests-race
ARG TESTPKGS=./...
ENV CGO_ENABLED=1
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    --mount=type=cache,target=/tmp,id=omni-infra-provider-ovhcloud/tmp \
    go test -race ${TESTPKGS}

# builds omni-infra-provider-ovhcloud-linux-amd64
FROM base AS omni-infra-provider-ovhcloud-linux-amd64-build
COPY --from=generate / /
WORKDIR /src/cmd/omni-infra-provider-ovhcloud
ARG GO_BUILDFLAGS=-trimpath
ARG GO_LDFLAGS="-s -w"
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    GOARCH=amd64 GOOS=linux go build ${GO_BUILDFLAGS} -ldflags "${GO_LDFLAGS}" \
        -o /omni-infra-provider-ovhcloud-linux-amd64

# builds omni-infra-provider-ovhcloud-linux-arm64
FROM base AS omni-infra-provider-ovhcloud-linux-arm64-build
COPY --from=generate / /
WORKDIR /src/cmd/omni-infra-provider-ovhcloud
ARG GO_BUILDFLAGS=-trimpath
ARG GO_LDFLAGS="-s -w"
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    GOARCH=arm64 GOOS=linux go build ${GO_BUILDFLAGS} -ldflags "${GO_LDFLAGS}" \
        -o /omni-infra-provider-ovhcloud-linux-arm64

FROM scratch AS omni-infra-provider-ovhcloud-linux-amd64
COPY --from=omni-infra-provider-ovhcloud-linux-amd64-build /omni-infra-provider-ovhcloud-linux-amd64 /omni-infra-provider-ovhcloud-linux-amd64

FROM scratch AS omni-infra-provider-ovhcloud-linux-arm64
COPY --from=omni-infra-provider-ovhcloud-linux-arm64-build /omni-infra-provider-ovhcloud-linux-arm64 /omni-infra-provider-ovhcloud-linux-arm64

# composite scratch stage carrying both per-arch binaries (used by release artifact extraction)
FROM scratch AS omni-infra-provider-ovhcloud-all
COPY --from=omni-infra-provider-ovhcloud-linux-amd64 / /
COPY --from=omni-infra-provider-ovhcloud-linux-arm64 / /

# dispatched runtime binary, chooses the right arch based on TARGETARCH
FROM omni-infra-provider-ovhcloud-linux-${TARGETARCH} AS omni-infra-provider-ovhcloud

# runtime image
FROM scratch AS image-omni-infra-provider-ovhcloud
ARG TARGETARCH
COPY --from=omni-infra-provider-ovhcloud omni-infra-provider-ovhcloud-linux-${TARGETARCH} /omni-infra-provider-ovhcloud
COPY --from=image-fhs / /
COPY --from=image-ca-certificates / /
LABEL org.opencontainers.image.source=https://github.com/ktijssen/omni-infra-provider-ovhcloud
ENTRYPOINT ["/omni-infra-provider-ovhcloud"]
