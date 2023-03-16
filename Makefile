ARCH ?= amd64
BIN = k8s-netperf 
BIN_DIR = bin
BIN_PATH = $(BIN_DIR)/$(ARCH)/$(BIN)
CGO = 0

all: build

build: $(BIN_PATH)

clean: $(BIN_PATH) 
	rm -rf bin/$(ARCH)

$(BIN_PATH): $(SOURCES)
	GOARCH=$(ARCH) CGO_ENABLED=$(CGO) go build -v -mod vendor -o $(BIN_PATH) ./cmd/k8s-netperf
