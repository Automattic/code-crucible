GO ?= go
GOCACHE ?= $(CURDIR)/.cache/go-build
GOMODCACHE ?= $(CURDIR)/.cache/gomod
GOENV := GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE)

.PHONY: build test vet fmt examples-test check smoke smoke-gateway-network-podman smoke-gateway-network-docker regression-tournament clean

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
	./bin/crucible run --project-dir examples/go-ranking-poc --task-file task.md --source-path ranking/rank.go --evaluator-script evaluator.sh --variants 1 --external-mode deny
	./bin/crucible evaluate --project-dir examples/go-ranking-poc --candidate candidate-0000-baseline --timeout 60s --require-passed
	./bin/crucible leaderboard --project-dir examples/go-ranking-poc

smoke-gateway-network-podman: build
	./bin/crucible run --project-dir examples/gateway-network-poc --task-file task.md --source-path candidate.go --evaluator-script evaluator.sh --variants 1 --external-mode mock --external-fixtures fixtures/http-fixtures.json
	./bin/crucible evaluate --project-dir examples/gateway-network-poc --candidate candidate-0000-baseline --timeout 120s --sandbox-engine podman --sandbox-image golang:1.22 --sandbox-profile networked --external-routing gateway-network --require-passed
	./bin/crucible leaderboard --project-dir examples/gateway-network-poc

smoke-gateway-network-docker: build
	./bin/crucible run --project-dir examples/gateway-network-poc --task-file task.md --source-path candidate.go --evaluator-script evaluator.sh --variants 1 --external-mode mock --external-fixtures fixtures/http-fixtures.json
	./bin/crucible evaluate --project-dir examples/gateway-network-poc --candidate candidate-0000-baseline --timeout 120s --sandbox-engine docker --sandbox-image golang:1.22 --sandbox-profile networked --external-routing gateway-network --require-passed
	./bin/crucible leaderboard --project-dir examples/gateway-network-poc

regression-tournament: smoke
	./bin/crucible index --project-dir examples/go-ranking-poc
	./bin/crucible query candidates --project-dir examples/go-ranking-poc --status passed --limit 5
	./bin/crucible report --project-dir examples/go-ranking-poc

clean:
	rm -rf bin dist .cache
	rm -rf examples/go-ranking-poc/.crucible examples/go-ranking-poc/.codex
	rm -rf examples/gateway-network-poc/.crucible examples/gateway-network-poc/.codex
