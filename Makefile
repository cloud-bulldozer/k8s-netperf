# -------------------------------------------------------------
# This makefile defines the following targets
#
#   - all (default) - builds all targets
#   - build - builds k8s-netperf binary
#   - container-build - builds the container image 
#	- gha-build	- build multi-architecture container image
#	- gha-push - Push the image & manifest
#   - clean - remove everything under bin/* directory
#   - verify - alias for verify-fast (developer quick check)
#   - verify-fast - static checks only (gofmt + golangci-lint)
#   - verify-ci - CI bundle: verify-fast + unit tests
#   - verify-go - Go static checks (gofmt + golangci-lint)
#   - verify-gofmt - verifies Go files are gofmt -s formatted
#   - update-gofmt - formats Go files with gofmt -s
#   - verify-golangci - runs golangci-lint
#   - test - runs Go unit tests

ARCH=$(shell go env GOARCH)
BIN = k8s-netperf
BIN_DIR = bin
BIN_PATH = $(BIN_DIR)/$(ARCH)/$(BIN)
CGO = 0
RHEL_VERSION = ubi9
CONTAINER ?= podman
CONTAINER_BUILD ?= podman build --force-rm
CONTAINER_NS ?= quay.io/cloud-bulldozer
GOFMT ?= gofmt
GOLANGCI_LINT ?= golangci-lint
SOURCES := $(shell find . -type f -name '*.go' -not -path './vendor/*' -not -path './bin/*')

# k8s-netperf version
GIT_COMMIT = $(shell git rev-parse HEAD)
BUILD_DATE = $(shell date '+%Y-%m-%d-%H:%M:%S')
CMD_VERSION= github.com/cloud-bulldozer/go-commons/v2/version
VERSION = $(shell branch="$$(git rev-parse --abbrev-ref HEAD)"; \
	if [ "$$branch" = "HEAD" ]; then \
		git describe --tags --abbrev=0 2>/dev/null || git rev-parse --short HEAD; \
	else \
		echo "$$branch"; \
	fi)

.PHONY: all build container-build gha-build gha-push clean verify verify-ci verify-fast verify-go verify-gofmt update-gofmt verify-golangci test

all: build container-build

build: $(BIN_PATH)

container-build: build
	@echo "Building the container image"
	$(CONTAINER_BUILD) -f containers/Containerfile \
		--build-arg RHEL_VERSION=$(RHEL_VERSION) \
		-t $(CONTAINER_NS)/$(BIN):latest ./containers

gha-build:
	@echo "Building the container image for GHA"
	$(CONTAINER_BUILD) -f containers/Containerfile \
		--build-arg RHEL_VERSION=$(RHEL_VERSION) --platform=linux/amd64,linux/arm64,linux/ppc64le,linux/s390x \
		./containers --manifest=$(CONTAINER_NS)/${BIN}:latest

gha-push:
	@echo "Pushing Container Images & manifest"
	$(CONTAINER) manifest push $(CONTAINER_NS)/${BIN}:latest $(CONTAINER_NS)/${BIN}:latest

clean:
	rm -rf bin/$(ARCH)

verify: verify-fast

verify-fast: verify-go

verify-ci: verify-fast test

verify-go: verify-gofmt verify-golangci

verify-gofmt:
	@files="$$($(GOFMT) -s -l $(SOURCES))"; status=$$?; \
	if [ $$status -ne 0 ]; then \
		echo "Failed to run gofmt check"; \
		exit $$status; \
	fi; \
	if [ -n "$$files" ]; then \
		echo "Go files are not formatted. Run: make update-gofmt"; \
		echo "$$files"; \
		exit 1; \
	fi

update-gofmt:
	$(GOFMT) -s -w $(SOURCES)

verify-golangci:
	$(GOLANGCI_LINT) run --timeout=5m

test:
	go test -v ./...

$(BIN_PATH): $(SOURCES)
	GOARCH=$(ARCH) CGO_ENABLED=$(CGO) go build -v -ldflags "-X $(CMD_VERSION).GitCommit=$(GIT_COMMIT) -X $(CMD_VERSION).BuildDate=$(BUILD_DATE) -X $(CMD_VERSION).Version=$(VERSION)" -o $(BIN_PATH) ./cmd/k8s-netperf
