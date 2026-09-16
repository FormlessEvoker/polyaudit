.PHONY: build test check fmt
VERSION ?= dev

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/polyaudit ./cmd/polyaudit

test:
	go test -race ./...

check:
	go vet ./...
	go test -race ./...
	@test -z "$$(gofmt -l cmd internal)"

fmt:
	gofmt -w cmd internal
