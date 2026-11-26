.PHONY: all build build-cli build-lib test test-go test-py clean

GO ?= $(shell which go 2>/dev/null || echo $(HOME)/.local/bin/go)
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Darwin)
  LIB_EXT := dylib
else
  LIB_EXT := so
endif

all: build

build: build-cli build-lib

build-cli:
	mkdir -p bin
	$(GO) build -o bin/rulemma ./cmd/rulemma

build-lib:
	$(GO) build -buildmode=c-shared -o python/rulemma/librulemma.$(LIB_EXT) ./capi
	rm -f python/rulemma/librulemma.h

test: test-go test-py

test-go:
	$(GO) test -v -race ./...

test-py: build-lib
	PYTHONPATH=python python3 -m unittest discover -s tests -v

clean:
	rm -rf bin/ python/rulemma/librulemma.* python/rulemma/data/ build/ dist/ *.egg-info
