DIST ?= dist/

PLATFORMS := darwin/arm64 linux/amd64

.PHONY: all build test integration-test clean lint fmt dist dequarantine

all: build test

build:
	CGO_ENABLED=0 go build -o bin/ ./cmd/laterm/

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
	@mkdir -p "$(DIST)"
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*}; \
		GOARCH=$${platform#*/}; \
		output="$(DIST)/laterm-$${GOOS}-$${GOARCH}"; \
		[[ "$$GOOS" != "windows" ]] || output="$${output}.exe"; \
		echo "Building $$output ..."; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH \
			go build -o "$$output" ./cmd/laterm/; \
	done

dequarantine:
	xattr -d com.apple.quarantine $(DIST)/*darwin*
