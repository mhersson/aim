.PHONY: help build build-darwin run test test-changed test-race doc-tests vet lint fmt fmt-check tidy-check scan scan-commits check-fast check-fast-full check install clean

help:
	@echo "Targets:"
	@echo "  help         show this list"
	@echo "  build        build bin/aim"
	@echo "  build-darwin cross-compile everything for darwin"
	@echo "  run          run the app; pass arguments with ARGS=..."
	@echo "  test         run the tests"
	@echo "  test-changed run the tests of the changed packages; TEST_PKGS=... overrides"
	@echo "  test-race    run the tests with the race detector"
	@echo "  doc-tests    run the doc tests: TestKeysPage, TestGuideLinks, TestConfigExample and TestReadmeKeys"
	@echo "  vet          go vet, with and without the bench and live tags"
	@echo "  lint         golangci-lint run"
	@echo "  fmt          format the code with golangci-lint fmt"
	@echo "  fmt-check    fail and show the diff if formatting is needed"
	@echo "  tidy-check   fail if go mod tidy would change go.mod or go.sum"
	@echo "  scan         fail on private-use glyphs in Go, TOML and Markdown files, em-dashes in Go, Markdown and TOML files and wall-clock bounds in tests"
	@echo "  scan-commits fail on IDs, session links, em-dashes and big blobs in SCAN_BASE..SCAN_HEAD (origin/main..HEAD)"
	@echo "  check-fast   the per-commit check: build, fmt-check, vet, lint, test-changed, scan; a change of non-code files only runs scan and the doc tests"
	@echo "  check        the Linux go-checks gates of CI: build, build-darwin, fmt-check, vet, lint, test-race, tidy-check, scan"
	@echo "  install      go install the binary into GOBIN, else GOPATH/bin"
	@echo "  release-dry  the GoReleaser dry run (needs GoReleaser 2): snapshot archives, checksums and binaries, then the changelog and notes in a scratch clone"
	@echo "  clean        remove bin/ and dist/"

GO_LDFLAGS = -s -w

build:
	go build -trimpath -ldflags='$(GO_LDFLAGS)' -o bin/aim ./cmd/aim

build-darwin:
	GOOS=darwin go build ./...

run:
	go run ./cmd/aim $(ARGS)

test:
	go test ./...

# A failing package lookup tests everything rather than nothing.
test-changed:
	@pkgs="$(TEST_PKGS)"; \
	if [ -z "$$pkgs" ]; then pkgs=$$(sh scripts/changed-packages.sh) || pkgs=./...; fi; \
	if [ -z "$$pkgs" ]; then echo "test-changed: no Go package changed"; exit 0; fi; \
	echo "go test $$pkgs"; \
	go test $$pkgs

test-race:
	CGO_ENABLED=1 go test -race ./...

doc-tests:
	go test -run '^(TestKeysPage|TestGuideLinks|TestConfigExample|TestReadmeKeys)$$' ./internal/ui ./cmd/aim

vet:
	go vet ./...
	go vet -tags bench,live ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

fmt-check:
	@out=$$(golangci-lint fmt --diff 2>&1) && rc=0 || rc=$$?; \
	if [ $$rc -ne 0 ] || [ -n "$$out" ]; then \
		printf '%s\n' "$$out"; \
		echo "fmt-check: formatting needed; run 'make fmt'"; \
		exit 1; \
	fi

tidy-check:
	go mod tidy -diff

# Files come from git so a new file is caught before it is added; the list goes
# through a temp file so a failing git fails the scan. LC_ALL=C byte patterns
# because macOS grep has no -P. The canary proves grep can match before an empty
# result is trusted; /dev/null keeps grep off stdin on an empty list. Recipes
# stay correct under sh -e (BSD make).
scan:
	@export LC_ALL=C; \
	list=$$(mktemp "$${TMPDIR:-/tmp}/scan.XXXXXX") || exit 1; \
	trap 'rm -f "$$list"' EXIT; \
	glyph=$$(printf '\356[\200-\277][\200-\277]|\357[\200-\243][\200-\277]|\363[\260-\277][\200-\277][\200-\277]'); \
	dash=$$(printf '\342\200\224'); \
	printf '\357\200\200\n' | grep -qE "$$glyph" || { echo "scan: grep cannot match the glyph pattern"; exit 1; }; \
	printf '\342\200\224\n' | grep -qF "$$dash" || { echo "scan: grep cannot match an em-dash"; exit 1; }; \
	git ls-files -z --cached --others --exclude-standard -- '*.go' '*.toml' '*.md' ':(exclude)docs/superpowers' > "$$list" || exit 1; \
	bad=$$(xargs -0 grep -IlE "$$glyph" /dev/null < "$$list") || :; \
	if [ -n "$$bad" ]; then \
		echo "scan: private-use glyphs in:"; \
		printf '%s\n' "$$bad"; \
		fail=1; \
	fi; \
	git ls-files -z --cached --others --exclude-standard -- '*.go' '*.md' '*.toml' ':(exclude)docs/superpowers' > "$$list" || exit 1; \
	bad=$$(xargs -0 grep -IlF "$$dash" /dev/null < "$$list") || :; \
	if [ -n "$$bad" ]; then \
		echo "scan: em-dashes (use hyphens) in:"; \
		printf '%s\n' "$$bad"; \
		fail=1; \
	fi; \
	sh scripts/scan-wall-clock.sh || fail=1; \
	[ -z "$$fail" ]

scan-commits:
	@sh scripts/scan-commits.sh "$(SCAN_BASE)" "$(SCAN_HEAD)"

# A commit whose every changed file is non-code (scripts/code-changed.sh) runs
# only the scan and the doc tests. TEST_PKGS set means the full check.
check-fast:
	@if [ -n "$(TEST_PKGS)" ] || sh scripts/code-changed.sh; then \
		$(MAKE) --no-print-directory check-fast-full; \
	else \
		echo "check-fast: no code changed; running only the scan and the doc tests"; \
		$(MAKE) --no-print-directory scan doc-tests; \
	fi

check-fast-full: build fmt-check vet lint test-changed scan

check: build build-darwin fmt-check vet lint test-race tidy-check scan

install:
	go install -trimpath -ldflags='$(GO_LDFLAGS)' ./cmd/aim

.PHONY: release-dry
release-dry:
	sh scripts/release-check.sh
	sh scripts/release-notes-check.sh

clean:
	rm -rf bin dist
