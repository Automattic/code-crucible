.PHONY: build test vet fmt check

build:
	go build -o bin/crucible ./cmd/crucible

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

check: test vet build
