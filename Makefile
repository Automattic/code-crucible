.PHONY: build test vet fmt examples-test check

build:
	go build -o bin/crucible ./cmd/crucible

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal examples/go-ranking-poc/ranking

examples-test:
	cd examples/go-ranking-poc && go test ./...

check: test vet build examples-test
