.PHONY: build build-all clean test install

# Build for current platform
build:
	go build -ldflags="-s -w" -o statusline

# Build for all platforms
build-all: clean
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/statusline-linux-amd64
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/statusline-linux-arm64
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/statusline-darwin-amd64
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/statusline-darwin-arm64

# Clean build artifacts
clean:
	rm -f statusline
	rm -rf bin/

# Run tests
test:
	go test -v ./...

# Install to ~/.claude/
install: build
	mkdir -p ~/.claude
	cp statusline ~/.claude/statusline
	chmod +x ~/.claude/statusline
	@echo "Installed to ~/.claude/statusline"
	@echo "Update your settings.json:"
	@echo '  "statusLine": {'
	@echo '    "type": "command",'
	@echo '    "command": "~/.claude/statusline"'
	@echo '  }'
