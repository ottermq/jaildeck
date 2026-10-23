BINARY_NAME=jaildeck
MAIN_PATH=cmd/jaildeck
BUILD_DIR=bin
COVER_PROFILE=$(BUILD_DIR)/coverage.out

GO?=go

# Installation paths. PREFIX is where FreeBSD puts local software; DESTDIR
# stages the same layout somewhere else, which is what a package build wants.
PREFIX?=/usr/local
DESTDIR?=

.PHONY: build run run-fake test test-race cover cover-html vet fmt fmt-check tidy-check check clean help web web-dev web-check all install

## build: compile the binary into bin/
build:
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -o ./$(BUILD_DIR)/$(BINARY_NAME) ./$(MAIN_PATH)

## run: build and run against the real FreeBSD adapter (requires FreeBSD)
run: build
	@./$(BUILD_DIR)/$(BINARY_NAME)

## run-fake: build and run against the in-memory adapter (works on any OS)
run-fake: build
	@JAILDECK_ADAPTER=fake ./$(BUILD_DIR)/$(BINARY_NAME)

## test: run the unit test suite
test:
	@$(GO) test ./...

## test-race: run the unit test suite under the race detector
test-race:
	@$(GO) test -race ./...

## cover: run tests and report coverage per package
cover:
	@mkdir -p $(BUILD_DIR)
	@$(GO) test -coverprofile=$(COVER_PROFILE) ./...
	@$(GO) tool cover -func=$(COVER_PROFILE) | tail -n 1

## cover-html: write an HTML coverage report to bin/coverage.html
cover-html: cover
	@$(GO) tool cover -html=$(COVER_PROFILE) -o $(BUILD_DIR)/coverage.html
	@echo "wrote $(BUILD_DIR)/coverage.html"

## vet: run go vet
vet:
	@$(GO) vet ./...

## fmt: format all Go source in place
fmt:
	@gofmt -w .

## fmt-check: fail if any Go source is unformatted
fmt-check:
	@unformatted=$$(gofmt -l .);\
		if [ -n "$$unformatted" ]; then\
			echo "unformatted files:"; echo "$$unformatted"; exit 1;\
		fi

## tidy-check: fail if go.mod/go.sum are not tidy
tidy-check:
	@cp go.mod $(BUILD_DIR)/go.mod.bak 2>/dev/null || mkdir -p $(BUILD_DIR) && cp go.mod $(BUILD_DIR)/go.mod.bak
	@cp go.sum $(BUILD_DIR)/go.sum.bak
	@$(GO) mod tidy
	@if ! cmp -s go.mod $(BUILD_DIR)/go.mod.bak || ! cmp -s go.sum $(BUILD_DIR)/go.sum.bak; then\
		cp $(BUILD_DIR)/go.mod.bak go.mod; cp $(BUILD_DIR)/go.sum.bak go.sum;\
		echo "go.mod/go.sum are not tidy — run: $(GO) mod tidy"; exit 1;\
	fi
	@rm -f $(BUILD_DIR)/go.mod.bak $(BUILD_DIR)/go.sum.bak

## check: the full verification gate — formatting, vet, build, tests
check: fmt-check vet build test
	@echo "check: OK"

## install: install the binary, rc.d script and sample env under PREFIX (run on the FreeBSD host, or stage with DESTDIR)
install: build
	@install -d $(DESTDIR)$(PREFIX)/bin $(DESTDIR)$(PREFIX)/etc/rc.d $(DESTDIR)$(PREFIX)/etc/$(BINARY_NAME)
	@install -m 0555 ./$(BUILD_DIR)/$(BINARY_NAME) $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)
	@install -m 0555 ./rc.d/$(BINARY_NAME) $(DESTDIR)$(PREFIX)/etc/rc.d/$(BINARY_NAME)
	@# The sample only. A live jaildeck.env is the operator's and is never overwritten.
	@install -m 0644 ./.env.sample $(DESTDIR)$(PREFIX)/etc/$(BINARY_NAME)/$(BINARY_NAME).env.sample
	@echo "installed:"
	@echo "  $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)"
	@echo "  $(DESTDIR)$(PREFIX)/etc/rc.d/$(BINARY_NAME)"
	@echo "  $(DESTDIR)$(PREFIX)/etc/$(BINARY_NAME)/$(BINARY_NAME).env.sample"
	@echo "copy the sample to $(PREFIX)/etc/$(BINARY_NAME)/$(BINARY_NAME).env and edit it"

## clean: remove build artifacts
clean:
	@rm -rf $(BUILD_DIR)

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'

## web: build the web frontend
web:
	@cd web && npm ci && npm run build
	@touch web/dist/.gitkeep			# Vite emties dist/; the placeholder keeps //go:embed working

## web-dev: run the web frontend in development mode (requires Node.js)
web-dev:
	@cd web && npm install && npm run dev

## web-check: typecheck the frontend (requires Node)
web-check:
	@cd web && npm run check

## all: build the backend and frontend
all: web build