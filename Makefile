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
CONTAINER_BUILD ?= podman build --force-rm
CONTAINER_NS ?= quay.io/cloud-bulldozer/netperf

all: build container-build

build: $(BIN_PATH)

container-build: build
	@echo "Building the container image"
	$(CONTAINER_BUILD) -f containers/Containerfile \
		--build-arg RHEL_VERSION=$(RHEL_VERSION) \
		-t $(CONTAINER_NS)/$(BIN) ./containers

gha-build:
	@echo "Building the container image for GHA"
	$(CONTAINER_BUILD) -f containers/Containerfile \
		--build-arg RHEL_VERSION=$(RHEL_VERSION) --platform=linux/amd64,linux/arm64,linux/ppc64le,linux/s390x \
		-t $(CONTAINER_NS) ./containers --manifest=$(CONTAINER_NS):latest

gha-push: build gha-build
	@echo "Pushing Container Images & manifest"
	$(CONTAINER_BUILD) manifest push

clean: $(BIN_PATH) 
	rm -rf bin/$(ARCH)

$(BIN_PATH): $(SOURCES)
	GOARCH=$(ARCH) CGO_ENABLED=$(CGO) go build -v -o $(BIN_PATH) ./cmd/k8s-netperf
