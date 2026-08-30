VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
MAXGRAPH_VERSION ?= 0.24.0
ESBUILD_VERSION  ?= 0.28.2
LDFLAGS := -s -w -X github.com/imohiyoko/oekaki/internal/cli.Version=$(VERSION)
EXAMPLE  := examples/three-tier
COVERAGE := examples/log-coverage

.PHONY: all
all: test build

.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o oekaki ./cmd/oekaki

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi
	golangci-lint run ./...

.PHONY: install
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/oekaki

# Regenerate the checked-in example output. Because rendering is deterministic,
# this is a no-op unless behaviour actually changed — which is exactly what
# `make verify-example` relies on.
.PHONY: example
example:
	go run ./cmd/oekaki graph  $(EXAMPLE)/plan.json --source-dir $(EXAMPLE) -o $(EXAMPLE)/graph.json
	go run ./cmd/oekaki render $(EXAMPLE)/plan.json --source-dir $(EXAMPLE) --title "three-tier example" -o $(EXAMPLE)/architecture.svg
	go run ./cmd/oekaki render $(EXAMPLE)/plan.json -f mermaid -o $(EXAMPLE)/architecture.mmd
	python3 $(COVERAGE)/gen_plan.py
	go run ./cmd/oekaki graph $(COVERAGE)/plan.json --source-dir $(COVERAGE) \
	    --overlay $(COVERAGE)/overlay.json --overlay-report $(COVERAGE)/report.json \
	    -o $(COVERAGE)/graph.json
	# Rendered from graph.json rather than from the plan plus the overlay, on
	# purpose: it proves the IR carries coverage, claims and conflicts, which
	# is what every later consumer depends on.
	go run ./cmd/oekaki render $(COVERAGE)/graph.json --title "log coverage" --legend -o $(COVERAGE)/coverage.svg
	go run ./cmd/oekaki render $(COVERAGE)/graph.json -f mermaid -o $(COVERAGE)/coverage.mmd
	go run ./cmd/oekaki render $(COVERAGE)/graph.json --title "log coverage" -o $(COVERAGE)/coverage.html

# Rebuild the vendored maxGraph bundle from renderers/html/vendor/maxgraph.entry.js.
#
# Not part of any other target and not run in CI: the bundle is checked in so a
# clone builds with the Go toolchain alone, exactly like elk.bundled.js. Both
# versions are pinned, and esbuild is deterministic for the same input, so
# re-running this on the same versions reproduces the committed bytes.
.PHONY: vendor-maxgraph
vendor-maxgraph:
	@tmp=$$(mktemp -d) && \
	npm --prefix $$tmp install --silent --no-audit --no-fund --no-save \
	    @maxgraph/core@$(MAXGRAPH_VERSION) esbuild@$(ESBUILD_VERSION) && \
	cp renderers/html/vendor/maxgraph.entry.js $$tmp/entry.js && \
	$$tmp/node_modules/.bin/esbuild $$tmp/entry.js --bundle --format=iife --minify \
	    --legal-comments=none --outfile=renderers/html/vendor/maxgraph.bundled.js && \
	rm -rf $$tmp
	@echo "maxGraph $(MAXGRAPH_VERSION) -> renderers/html/vendor/maxgraph.bundled.js"

# Fails if regenerating the example changes it, which catches both accidental
# output changes and a loss of determinism.
.PHONY: verify-example
verify-example: example
	git diff --exit-code -- $(EXAMPLE) $(COVERAGE)

.PHONY: clean
clean:
	rm -rf dist out oekaki oekaki.exe
