.DEFAULT_GOAL:=help
.PHONY: all build build-all test clean help

all: test build ## Run test, then build

build: ## Build the binary
	mkdir -p build
	go build -o build/engram ./cmd/engram

build-all: ## Build binary for Windows, Mac, Linux
	mkdir -p build/dist
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/engram.exe ./cmd/engram
	cd build && zip dist/engram_windows_amd64.zip engram.exe && rm engram.exe
	
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o build/engram ./cmd/engram
	cd build && tar -czf dist/engram_mac_amd64.tar.gz engram && rm engram
	
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o build/engram ./cmd/engram
	cd build && tar -czf dist/engram_mac_arm64.tar.gz engram && rm engram
	
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/engram ./cmd/engram
	cd build && tar -czf dist/engram_linux_amd64.tar.gz engram && rm engram

test: ## Run tests
	go test ./... -p 8

clean: ## Clean build artifacts
	rm -rf build

help: ## Display this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
