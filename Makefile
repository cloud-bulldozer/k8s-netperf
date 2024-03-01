# -------------------------------------------------------------
# This makefile defines the following targets
#
#   - all (default) - builds all targets
#   - build - builds k8s-netperf binary
#   - container-build - builds the container image 
#	- gha-build	- build multi-architecture container image
#	- gha-push - Push the image & manifest
#   - clean - remove everything under bin/* directory

ARCH=$(shell go env GOARCH)
BIN = k8s-netperf
BIN_DIR = bin
BIN_PATH = $(BIN_DIR)/$(ARCH)/$(BIN)
CGO = 0
RHEL_VERSION = ubi9
CONTAINER ?= podman
CONTAINER_BUILD ?= podman build --force-rm
CONTAINER_NS ?= quay.io/cloud-bulldozer
SOURCES := $(shell find . -type f -name "*.go")

# k8s-netperf version
GIT_COMMIT = $(shell git rev-parse HEAD)
BUILD_DATE = $(shell date '+%Y-%m-%d-%H:%M:%S')
CMD_VERSION= github.com/cloud-bulldozer/go-commons/version
BRANCH := $(shell git rev-parse --abbrev-ref HEAD)
ifeq ($(BRANCH),HEAD)
	VERSION := $(shell git describe --tags --abbrev=0)
else
	VERSION := $(BRANCH)
endif

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

gha-push: gha-build
	@echo "Pushing Container Images & manifest"
	$(CONTAINER) manifest push $(CONTAINER_NS)/${BIN}:latest $(CONTAINER_NS)/${BIN}:latest

clean:
	rm -rf bin/$(ARCH)

$(BIN_PATH): $(SOURCES)
	GOARCH=$(ARCH) CGO_ENABLED=$(CGO) go build -v -ldflags "-X $(CMD_VERSION).GitCommit=$(GIT_COMMIT) -X $(CMD_VERSION).BuildDate=$(BUILD_DATE) -X $(CMD_VERSION).Version=$(VERSION)" -o $(BIN_PATH) ./cmd/k8s-netperf
