# laterm — Rust build convenience wrapper over cargo.
# (The Go research prototype keeps its own Makefile under prototype/.)
#
# Release targets. Cross-compiling all three from one host needs the target's
# linker/toolchain; in practice build each on its native OS (CI does this on
# tags via .github/workflows/release.yml) or run `make dist` on each machine.
TARGETS := aarch64-apple-darwin x86_64-unknown-linux-gnu x86_64-pc-windows-gnu
DIST    := dist

.PHONY: all setup build release test fmt lint check clean dist install

all: build test

## setup: install rustup targets + cargo-zigbuild for cross-compiling.
## Requires zig (brew install zig) — zig provides the cross-linker so Rust
## cross-compiles from any host (like Go/zig), incl. Mac → Linux/Windows.
setup:
	rustup target add $(TARGETS)
	cargo install cargo-zigbuild || true
	@command -v zig >/dev/null || echo "NOTE: install zig (brew install zig) for cross-compilation"

## build: debug build for the host
build:
	cargo build

## release: optimized build for the host (target/release/laterm)
release:
	cargo build --release

## test: run the unit tests
test:
	cargo test

## fmt: format the source
fmt:
	cargo fmt

## lint: clippy across all targets
lint:
	cargo clippy --all-targets

## check: type-check for every release target (validates cross-platform cfg)
check:
	@for t in $(TARGETS); do echo "check $$t"; cargo check --target $$t || exit 1; done

## clean: remove build artifacts
clean:
	cargo clean
	rm -rf $(DIST)

## dist: cross-build every target via cargo-zigbuild and stage binaries in
## dist/. Works from any host (needs zig + cargo-zigbuild; run `make setup`).
dist:
	@mkdir -p $(DIST)
	@for t in $(TARGETS); do \
		echo "==> $$t"; \
		if cargo zigbuild --release --target $$t; then \
			ext=""; case $$t in *windows*) ext=".exe";; esac; \
			cp "target/$$t/release/laterm$$ext" "$(DIST)/laterm-$$t$$ext"; \
			echo "    staged $(DIST)/laterm-$$t$$ext"; \
		else \
			echo "    skipped $$t (toolchain unavailable on this host)"; \
		fi; \
	done

run-build: build
	./target/debug/laterm

INSTALL_DIR ?= $(HOME)/.local/bin
OS := $(shell uname -s)
install:
	@[[ -d "$(INSTALL_DIR)" ]] || mkdir -pv "$(INSTALL_DIR)"
	@case "$(OS)" in \
	Darwin) echo "Installing $(OS) version to $(INSTALL_DIR) and dequarantining."; \
		      /bin/rm -f "$(INSTALL_DIR)/laterm"; \
	        cp -v dist/laterm-aarch64-apple-darwin "$(INSTALL_DIR)/laterm"; \
					xattr -d com.apple.quarantine "$(INSTALL_DIR)/laterm" 2> /dev/null || true;; \
	Linux) echo "Installing $(OS) version to $(INSTALL_DIR)"; \
	      cp -vf dist/laterm-x86_64-unknown-linux-gnu "$(INSTALL_DIR)/laterm";; \
	*) echo "Installing $(OS) version to $(INSTALL_DIR)"; \
	    cp -vf dist/laterm-x86_64-pc-windows-gnu.exe "$(INSTALL_DIR)/laterm.exe";; \
	esac

dequarantine:
	xattr -d com.apple.quarantine $(DIST)/*darwin*
