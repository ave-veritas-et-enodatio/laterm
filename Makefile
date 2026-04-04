OUT ?= bin/laterm
DIST ?= dist/laterm-darwin-arm64

.PHONY: all build test integration-test clean lint fmt dist

all: build test

build:
	CGO_ENABLED=0 go build -o $(OUT) ./cmd/laterm/

test:
	go test ./...

integration-test:
	go test -tags integration ./...

clean:
	rm -rf bin/

lint:
	go vet ./...

fmt:
	gofmt -w .

dist:
	@mkdir -p $(dir $(DIST))
	@$(MAKE) OUT=$(DIST) build
