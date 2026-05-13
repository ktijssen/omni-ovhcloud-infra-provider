# syntax = docker/dockerfile-upstream:1.20.0-labs

ARG TOOLCHAIN=docker.io/golang:1.26-alpine

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
RUN --mount=type=cache,target=/root/.cache/go-build,id=omni-infra-provider-ovhcloud/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg,id=omni-infra-provider-ovhcloud/go/pkg \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
 && mv /go/bin/protoc-gen-go /bin/protoc-gen-go

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

FROM scratch AS omni-infra-provider-ovhcloud-linux-amd64
COPY --from=omni-infra-provider-ovhcloud-linux-amd64-build /omni-infra-provider-ovhcloud-linux-amd64 /omni-infra-provider-ovhcloud-linux-amd64

FROM omni-infra-provider-ovhcloud-linux-amd64 AS omni-infra-provider-ovhcloud

FROM scratch AS omni-infra-provider-ovhcloud-all
COPY --from=omni-infra-provider-ovhcloud-linux-amd64 / /

FROM scratch AS image-omni-infra-provider-ovhcloud
COPY --from=omni-infra-provider-ovhcloud omni-infra-provider-ovhcloud-linux-amd64 /omni-infra-provider-ovhcloud
COPY --from=image-fhs / /
COPY --from=image-ca-certificates / /
LABEL org.opencontainers.image.source=https://github.com/ktijssen/omni-infra-provider-ovhcloud
ENTRYPOINT ["/omni-infra-provider-ovhcloud"]
