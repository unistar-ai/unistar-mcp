TAG?=$(shell git describe --abbrev=0 --tags 2>/dev/null || echo "v0.0.0" )
COMMIT?=$(shell git rev-parse HEAD)

default: build

.PHONY: build
build:
	COMMIT=$(COMMIT) TAG=$(TAG) goreleaser build --snapshot --clean

.PHONY: verify
verify:
	./scripts/verify.sh

.PHONY: test
test:
	CGO_ENABLED=0 go test -cover --count=1 ./...

.PHONY: integration-test
integration-test:
	./scripts/integration-test.sh

.PHONY: clean
clean:
	./scripts/clean.sh

.PHONY: image
image:
	TAG=$(TAG) ./scripts/image.sh

.PHONY: help
help:
	@echo "Usage:"
	@echo "	make build		build binary files"
	@echo "	make verify		verify modules"
	@echo "	make test		run unit tests"
	@echo "	make integration-test		run integration tests"
	@echo "	make image		build image (local dev purpose only)"
	@echo "	make clean		clean up built files"
	@echo "	make help		show this message"
