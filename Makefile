# Get the latest commit branch, hash, and date
TAG=$(shell git describe --tags --abbrev=0 --exact-match 2>/dev/null)
BRANCH=$(if $(TAG),$(TAG),$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null))
HASH=$(shell git rev-parse --short=7 HEAD 2>/dev/null)
TIMESTAMP=$(shell git log -1 --format=%ct HEAD 2>/dev/null | xargs -I{} date -u -r {} +%Y%m%dT%H%M%S)
GIT_REV=$(shell printf "%s-%s-%s" "$(BRANCH)" "$(HASH)" "$(TIMESTAMP)")
REV=$(if $(filter --,$(GIT_REV)),latest,$(GIT_REV))

all: test build

build:
	cd cmd/ralphex && go build -ldflags "-X main.revision=$(REV) -s -w" -o ../../.bin/ralphex.$(BRANCH)
	cp .bin/ralphex.$(BRANCH) .bin/ralphex

test:
	go clean -testcache
	go test -race -coverprofile=coverage.out ./...
	grep -v "_mock.go" coverage.out | grep -v mocks > coverage_no_mocks.out
	go tool cover -func=coverage_no_mocks.out
	rm coverage.out coverage_no_mocks.out

lint:
	golangci-lint run --max-issues-per-linter=0 --max-same-issues=0

fmt:
	gofmt -s -w $$(find . -type f -name "*.go" -not -path "./vendor/*" -not -path "./mocks/*" -not -path "**/mocks/*")
	goimports -w $$(find . -type f -name "*.go" -not -path "./vendor/*" -not -path "./mocks/*" -not -path "**/mocks/*")

race:
	go test -race -timeout=60s ./...

version:
	@echo "branch: $(BRANCH), hash: $(HASH), timestamp: $(TIMESTAMP)"
	@echo "revision: $(REV)"

e2e-setup:
	go run github.com/playwright-community/playwright-go/cmd/playwright@latest install --with-deps chromium

e2e:
	go test -v -failfast -count=1 -timeout=5m -tags=e2e ./e2e/...

e2e-ui:
	E2E_HEADLESS=false go test -v -failfast -count=1 -timeout=10m -tags=e2e ./e2e/...

e2e-prep: build
	@./scripts/prep-toy-test.sh
	@cp .bin/ralphex /tmp/ralphex-test/.bin/ralphex
	@echo ""
	@echo "=== E2E Full Test Ready ==="
	@echo "cd /tmp/ralphex-test"
	@echo ".bin/ralphex docs/plans/fix-issues.md"
	@echo ""
	@echo "Monitor: tail -f /tmp/ralphex-test/progress-fix-issues.txt"

e2e-review: build
	@./scripts/prep-review-test.sh
	@cp .bin/ralphex /tmp/ralphex-review-test/.bin/ralphex
	@echo ""
	@echo "=== E2E Review Test Ready ==="
	@echo "cd /tmp/ralphex-review-test"
	@echo ".bin/ralphex --review"
	@echo ""
	@echo "Monitor: tail -f /tmp/ralphex-review-test/progress-review.txt"

e2e-codex: build
	@./scripts/prep-review-test.sh
	@cp .bin/ralphex /tmp/ralphex-review-test/.bin/ralphex
	@echo ""
	@echo "=== E2E Codex-Only Test Ready ==="
	@echo "cd /tmp/ralphex-review-test"
	@echo ".bin/ralphex --codex-only"
	@echo ""
	@echo "Monitor: tail -f /tmp/ralphex-review-test/progress-codex.txt"

prep_site:
	# prepare docs source directory for mkdocs
	rm -rf site/docs-src && mkdir -p site/docs-src
	cp -fv README.md site/docs-src/index.md
	cp -rv assets site/docs-src/
	grep -v -E 'badge|coveralls|goreportcard' site/docs-src/index.md > site/docs-src/index.md.tmp && mv site/docs-src/index.md.tmp site/docs-src/index.md
	mkdir -p site/docs-src/stylesheets && cp -fv site/docs/stylesheets/extra.css site/docs-src/stylesheets/
	# build site structure: landing page + docs subdirectory
	rm -rf site/site && mkdir -p site/site
	cp -fv site/docs/index.html site/site/
	cp -fv site/docs/favicon.png site/site/
	cp -rv assets site/site/
	cp -fv llms.txt site/site/
	# build mkdocs into site/site/docs/
	cd site && pip install -r requirements.txt && mkdocs build
	# copy raw claude assets (not rendered by mkdocs)
	rm -rf site/site/docs/assets/claude && cp -rv assets/claude site/site/docs/assets/

docker-build:
	docker build -t ghcr.io/umputun/ralphex:latest .

docker-run:
	./scripts/ralphex-dk.sh $(ARGS)

.PHONY: all build test lint fmt race version e2e-setup e2e e2e-ui e2e-prep e2e-review e2e-codex prep_site docker-build docker-run
