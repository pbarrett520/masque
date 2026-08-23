# On Linux, Wails needs the webkit2_41 tag when only webkit2gtk-4.1 is
# installed (modern distros); with webkit2gtk-4.0 present, no tag is needed.
WEBKIT_TAG := $(shell pkg-config --exists webkit2gtk-4.1 2>/dev/null && echo "-tags webkit2_41")

.PHONY: dev build test lint clean

dev: ## Run the app with hot reload
	wails dev $(WEBKIT_TAG)

build: ## Build a production binary into build/bin
	wails build $(WEBKIT_TAG)

test: ## Run Go tests
	go test ./...

lint: ## Run golangci-lint (install: https://golangci-lint.run/docs/welcome/install/)
	golangci-lint run

clean: ## Remove build artifacts
	rm -rf build/bin frontend/dist/* frontend/wailsjs
	touch frontend/dist/gitkeep
