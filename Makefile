.PHONY: all build build-cli build-lib build-wasm test test-go test-py test-examples check-manifest release-check clean

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
	rm -rf python/rulemma/data
	$(GO) build -buildmode=c-shared -o python/rulemma/librulemma.$(LIB_EXT) ./capi
	rm -f python/rulemma/librulemma.h

build-wasm:
	mkdir -p bin/wasm
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -o bin/wasm/rulemma-thin.wasm ./cmd/rulemma-wasm
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -tags "wasm_embed,rulemma_core" -o bin/wasm/rulemma-core.wasm ./cmd/rulemma-wasm
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -tags "wasm_embed" -o bin/wasm/rulemma-full.wasm ./cmd/rulemma-wasm
	@for f in bin/wasm/rulemma-thin.wasm bin/wasm/rulemma-core.wasm bin/wasm/rulemma-full.wasm; do \
		raw=$$(stat -c%s "$$f" 2>/dev/null || stat -f%z "$$f"); \
		gz=$$(gzip -c -9 "$$f" | wc -c); \
		printf "  %-30s raw=%5.2f MB  gzip-9=%5.2f MB\n" "$$f" "$$(awk "BEGIN{print $$raw/1048576}")" "$$(awk "BEGIN{print $$gz/1048576}")"; \
	done

check-manifest:
	python3 tools/update_manifest.py --check

release-check:
	python3 tools/release_check.py

test: check-manifest test-go test-py

test-go:
	$(GO) vet ./...
	$(GO) test -v -race ./...

test-py: build-cli build-lib
	PYTHONPATH=python python3 -m unittest discover -s tests -v

test-examples: build-cli build-lib
	PYTHONPATH=python python3 -m unittest tests/test_examples.py -v

clean:
	rm -rf bin/ python/rulemma/librulemma.* python/rulemma/data/ build/ dist/ *.egg-info
