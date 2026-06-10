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
        -ldflags="-extldflags='-static' -s -w -X github.com/STARRY-S/unistar-mcp/pkg/utils.Version=${TAG} -X github.com/STARRY-S/unistar-mcp/pkg/utils.GitCommit=${COMMIT}" \
        -o /unistar-mcp \
        ./cmd/

RUN /unistar-mcp -v

FROM alpine:latest

# Runtime dependencies: the server shells out to the GitHub CLI (all tools) and
# git (backport). github-cli lives in the Alpine community repository.
RUN apk add --no-cache git github-cli ca-certificates

WORKDIR /app
COPY --from=build /unistar-mcp /usr/local/bin

# Authenticate by passing a token at runtime, e.g.:
#   docker run -e GH_TOKEN=ghp_xxx unistar-mcp http --address :8080
ENTRYPOINT [ "unistar-mcp" ]
CMD [ "help" ]
