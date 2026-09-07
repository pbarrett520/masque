# On Linux, Wails needs the webkit2_41 tag when only webkit2gtk-4.1 is
# installed (modern distros); with webkit2gtk-4.0 present, no tag is needed.
WEBKIT     := $(shell pkg-config --exists webkit2gtk-4.1 2>/dev/null && echo webkit2_41)
WEBKIT_TAG := $(if $(WEBKIT),-tags $(WEBKIT))

.PHONY: dev build test lint clean appimage deb package-linux

dev: ## Run the app with hot reload
	wails dev $(WEBKIT_TAG)

build: ## Build a production binary into build/bin
	wails build $(WEBKIT_TAG) -trimpath

test: ## Run Go tests
	go test $(WEBKIT_TAG) ./...

lint: ## Run golangci-lint (install: https://golangci-lint.run/docs/welcome/install/)
	golangci-lint run $(if $(WEBKIT),--build-tags=$(WEBKIT))

appimage: build ## Bundle build/bin/masque as a self-contained AppImage (Linux)
	build/linux/appimage.sh

deb: build ## Package build/bin/masque as a .deb (Linux)
	build/linux/deb.sh

package-linux: appimage deb ## Both Linux packages

clean: ## Remove build artifacts
	rm -rf build/bin frontend/dist/* frontend/wailsjs
	touch frontend/dist/gitkeep
