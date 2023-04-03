# -------------------------------------------------------------
# This makefile defines the following targets
#
#   - all (default) - builds all targets
#   - build - builds k8s-netperf binary
#   - docker-build - builds the container image 
#   - clean - remove everything under bin/* directory

ARCH=$(shell go env GOARCH)
BIN = k8s-netperf 
BIN_DIR = bin
BIN_PATH = $(BIN_DIR)/$(ARCH)/$(BIN)
CGO = 0
RHEL_VERSION = ubi9
DOCKER_BUILD ?= podman build --force-rm
DOCKER_NS ?= perfscale

all: build docker-build

build: $(BIN_PATH)

docker-build: build
	@echo "Building the container image"
	$(DOCKER_BUILD) -f containers/Dockerfile \
		--build-arg RHEL_VERSION=$(RHEL_VERSION) \
		-t $(DOCKER_NS)/$(BIN) ./containers

clean: $(BIN_PATH) 
	rm -rf bin/$(ARCH)

$(BIN_PATH): $(SOURCES)
	GOARCH=$(ARCH) CGO_ENABLED=$(CGO) go build -v -mod vendor -o $(BIN_PATH) ./cmd/k8s-netperf
