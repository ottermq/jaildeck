BINARY_NAME=jaildeck
MAIN_PATH=cmd/jaildeck
BUILD_DIR=bin
COVER_PROFILE=$(BUILD_DIR)/coverage.out

GO?=go
LDFLAGS?=
VERSION?=
# The git revision a package is built from. Optional: package.sh asks git when
# it is empty, and a build from a `git archive` has no git to ask (#165).
COMMIT?=

# Where the API annotations live. swag is pinned in go.mod as a tool
# dependency, so `go tool swag` needs no global install and every machine runs
# the same version. Its --version flag reports a stale constant; go.mod is the
# truth.
SWAG_DIRS=cmd/jaildeck,internal/app,internal/jails,internal/host,internal/audit,internal/auth,internal/storage,internal/templates,internal/metrics,internal/firewall,internal/vms,internal/images,internal/networks,internal/console,internal/names,internal/about,internal/common
SPEC=docs/api/swagger.json

# Installation paths. PREFIX is where FreeBSD puts local software; DESTDIR
# stages the same layout somewhere else, which is what a package build wants.
PREFIX?=/usr/local
DESTDIR?=

# The make that package.sh calls back into. Passed through this name rather
# than written as $(MAKE) in the recipe, because GNU make runs any recipe line
# that mentions $(MAKE) even under -n, -t and -q -- right for a sub-make, which
# inherits -n and only prints, and wrong for a shell script, which does not.
# `gmake -n package` would otherwise build, stage and overwrite a real archive.
PACKAGE_MAKE=$(MAKE)

.PHONY: build run run-fake test test-race cover cover-html vet fmt fmt-check tidy-check check clean help web web-dev web-check web-test all install package doc doc-check

## build: compile the binary into bin/
build:
	@mkdir -p "$(BUILD_DIR)"
	@$(GO) build -ldflags "$(LDFLAGS)" -o "$(BUILD_DIR)/$(BINARY_NAME)" ./$(MAIN_PATH)

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
check: fmt-check vet build doc-check test
	@echo "check: OK"

## install: install the binary, rc.d script and sample env under PREFIX (run on the FreeBSD host, or stage with DESTDIR)
install: build
	@install -d "$(DESTDIR)$(PREFIX)/bin" "$(DESTDIR)$(PREFIX)/etc/rc.d" "$(DESTDIR)$(PREFIX)/etc/$(BINARY_NAME)"
	@install -m 0555 "$(BUILD_DIR)/$(BINARY_NAME)" "$(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)"
	@install -m 0555 ./rc.d/$(BINARY_NAME) "$(DESTDIR)$(PREFIX)/etc/rc.d/$(BINARY_NAME)"
	@# The sample only. A live jaildeck.env is the operator's and is never overwritten.
	@install -m 0644 ./.env.sample "$(DESTDIR)$(PREFIX)/etc/$(BINARY_NAME)/$(BINARY_NAME).env.sample"
	@echo "installed:"
	@echo "  $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)"
	@echo "  $(DESTDIR)$(PREFIX)/etc/rc.d/$(BINARY_NAME)"
	@echo "  $(DESTDIR)$(PREFIX)/etc/$(BINARY_NAME)/$(BINARY_NAME).env.sample"
	@echo "copy the sample to $(PREFIX)/etc/$(BINARY_NAME)/$(BINARY_NAME).env and edit it"

## package: build and verify bin/packages/jaildeck-VERSION.pkg (native FreeBSD 15 amd64)
package:
	@sh packaging/freebsd/package.sh "$(VERSION)" "$(PREFIX)" "$(GO)" "$(PACKAGE_MAKE)" "$(COMMIT)"

## clean: remove build artifacts
clean:
	@rm -rf $(BUILD_DIR)

## help: list available targets
help:
	@# Named, not $(MAKEFILE_LIST): that is GNU-only, and under FreeBSD's base
	@# make it expands to nothing and grep sits waiting on stdin.
	@grep -E '^## ' Makefile | sed 's/^## /  /'

## web: build the web frontend
web:
	@(cd web && npm ci && npm run build)
	@touch web/dist/.gitkeep			# Vite emties dist/; the placeholder keeps //go:embed working

## web-dev: run the web frontend in development mode (requires Node.js)
web-dev:
	@cd web && npm install && npm run dev

## web-check: typecheck and test the frontend (requires Node)
# Both, because the type check alone passed every bug v0.9.0's interface
# shipped: a binding's value is invisible to TypeScript (#77). Run from a
# subshell so make -j and BSD make's single-shell mode cannot run the two
# halves in the wrong directory.
web-check:
	@(cd web && npm run check && npm test)

## web-test: run the frontend tests alone (requires Node)
web-test:
	@(cd web && npm test)

## all: build the backend and frontend
# In order, never as prerequisites: `all: web build` lets make -j start the Go
# build while Vite has emptied dist/, and measured on isengard `gmake -j4 all`
# then exits 0 with a binary that carries no frontend. A sub-make is right
# here in a way it is not for package: it inherits -n and only prints.
all:
	@$(MAKE) web
	@$(MAKE) build

## doc: regenerate the OpenAPI spec from the handler annotations
doc:
	@mkdir -p $(dir $(SPEC))
	@$(GO) tool swag init -g main.go -d $(SWAG_DIRS) -o $(dir $(SPEC)) --ot json 2>&1 | grep -E "^.*(warning|error)" || true
	@echo "wrote $(SPEC)"

## doc-check: fail if the spec on disk is not what the annotations produce
# Generated into a temporary directory and compared with the file, rather than
# regenerated in place and compared with git. Comparing with git would fail
# whenever the annotations themselves are uncommitted -- which is most of the
# time while writing them -- and regenerating in place would make a check that
# modifies the working tree.
#
# The tracked guard is separate: git diff ignores files it has never seen, so a
# generated file absent from the commit would otherwise pass silently.
doc-check:
	@git ls-files --error-unmatch $(SPEC) >/dev/null 2>&1 || { \
		echo "$(SPEC) is not tracked by git. Run: make doc && git add $(SPEC)"; \
		exit 1; \
	}
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		$(GO) tool swag init -g main.go -d $(SWAG_DIRS) -o "$$tmp" --ot json >/dev/null 2>&1 && \
		if ! diff -q "$$tmp/swagger.json" $(SPEC) >/dev/null 2>&1; then \
			echo "$(SPEC) is out of date. Run: make doc"; \
			diff -u $(SPEC) "$$tmp/swagger.json" | head -20; \
			exit 1; \
		fi
