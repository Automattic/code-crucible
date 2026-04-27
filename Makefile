.PHONY: build test fmt

build:
	go build -o bin/crucible ./cmd/crucible

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal
