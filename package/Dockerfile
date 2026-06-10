FROM golang:1.26 AS build

WORKDIR /app

# Pre-download dependencies
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=bind,source=go.sum,target=go.sum \
    --mount=type=bind,source=go.mod,target=go.mod \
    go mod download -x

# Build the binary
ENV GOCACHE=/root/.cache/go-build
ARG TAG=v0.1.0-alpha
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target="/root/.cache/go-build" \
    --mount=type=bind,target=. \
    CGO_ENABLED=0 \
    go build \
        -buildmode=pie \
        -ldflags="-extldflags='-static' -s -w -X github.com/STARRY-S/unistar-mcp/pkg/utils.Version=${TAG} -X github.com/STARRY-S/unistar-mcp/pkg/utils.Commit=${COMMIT}" \
        -o /unistar-mcp \
        ./cmd/

RUN /unistar-mcp -v

FROM alpine:latest

WORKDIR /app
COPY config.yaml.example ./config.yaml
COPY --from=build /unistar-mcp /usr/local/bin

ENTRYPOINT [ "unistar-mcp" ]
CMD [ "help" ]
