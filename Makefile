BINARY := bin/loom
UI_DIR := web
NPM ?= npm
COVERAGE_MIN ?= 50

.PHONY: build test test-race vet fmt coverage coverage-all perf validate plan examples serve clean check ui ui-install e2e e2e-only lint hooks

build:
	go build -o $(BINARY) ./cmd/loom

test:
	go test ./...

test-race:
	CGO_ENABLED=1 go test -race ./...

vet:
	go vet ./...

lint: vet
	@command -v staticcheck >/dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed; run: go install honnef.co/go/tools/cmd/staticcheck@latest"

fmt:
	gofmt -w .

coverage:
	go test ./internal/engine -coverprofile=coverage.engine.out
	@go tool cover -func=coverage.engine.out | awk '$$1 == "total:" { gsub(/%/, "", $$3); if ($$3 < 80) { print "engine coverage below 80%"; exit 1 } }'

coverage-all:
	go test -count=1 ./... -coverprofile=coverage.out
	@go tool cover -func=coverage.out | awk -v min=$(COVERAGE_MIN) '$$1 == "total:" { gsub(/%/, "", $$3); if ($$3+0 < min+0) { printf "total coverage %s%% below %s%%\n", $$3, min; exit 1 } }'

perf:
	go test ./internal/engine -run '^TestPerformanceBudget$$' -count=1 -v

validate: build
	./$(BINARY) validate examples/*.json

plan: build
	./$(BINARY) plan examples/postgres-report.flow.json

examples: build
	@for f in examples/*.json; do ./$(BINARY) test --data .loom/examples "$$f"; done

serve: build
	./$(BINARY) serve

ui-install:
	cd $(UI_DIR) && $(NPM) install --no-audit --no-fund

ui: ui-install
	cd $(UI_DIR) && $(NPM) run build

e2e: ui
	cd $(UI_DIR) && $(NPM) run e2e:only

e2e-only:
	cd $(UI_DIR) && $(NPM) run e2e:only

check: fmt vet test-race coverage-all ui validate
	@echo "check passed (e2e and perf run in CI; use 'make e2e perf' locally)"

hooks:
	chmod +x .githooks/pre-commit .githooks/pre-push
	git config core.hooksPath .githooks
	@echo "hooks installed from .githooks"

clean:
	rm -rf bin web/dist/assets web/dist/index.html coverage.out coverage.engine.out
