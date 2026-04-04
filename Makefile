.PHONE all build clean dequarantine

all: build

dequarantine:
	@echo "Dequarantining..."
	xattr -d com.apple.quarantine bin/laterm-darwin-arm64
