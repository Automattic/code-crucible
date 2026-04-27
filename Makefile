GO ?= go
GOCACHE ?= $(CURDIR)/.cache/go-build
GOMODCACHE ?= $(CURDIR)/.cache/gomod
GOENV := GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE)

.PHONY: build test vet fmt examples-test check smoke

build:
	$(GOENV) $(GO) build -o bin/crucible ./cmd/crucible

test:
	$(GOENV) $(GO) test ./...

vet:
	$(GOENV) $(GO) vet ./...

fmt:
	gofmt -w ./cmd ./internal examples/go-ranking-poc/ranking

examples-test:
	cd examples/go-ranking-poc && $(GOENV) $(GO) test ./...

check: test vet build examples-test

smoke: build
	./bin/crucible run --project examples/go-ranking-poc --task-file task.md --target-path ranking/rank.go --evaluator-script evaluator.sh --variants 1 --external-mode deny
	./bin/crucible evaluate --project examples/go-ranking-poc --candidate candidate-0000-baseline --timeout 60s
	./bin/crucible leaderboard --project examples/go-ranking-poc
