DIST ?= dist/

PLATFORMS := darwin/arm64 linux/amd64

.PHONY: all build test integration-test clean lint fmt dist dequarantine agents

all: build test

AGENTS_VERSION ?= v0.1.0
AGENTS_REPO := git@github.com:ave-veritas-et-enodatio/agents.git
AGENTS_MARKER := .claude/agents/.git/HEAD
agents: $(AGENTS_MARKER)

$(AGENTS_MARKER):
		@cd .claude/ && \
		git clone $(AGENTS_REPO) && \
		cd agents && \
		git checkout $(AGENTS_VERSION) && \
		cd .. && \
		ln -s agents/commands .

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
