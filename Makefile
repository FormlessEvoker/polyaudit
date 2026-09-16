.PHONY: build test vet check ci fmt fmt-check snapshot release-check release-test
VERSION ?= dev

build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/polyaudit ./cmd/polyaudit

test:
	go test -race ./...

vet:
	go vet ./...

fmt-check:
	@unformatted="$$(gofmt -l cmd internal)"; status=$$?; \
	if [ $$status -ne 0 ]; then exit $$status; fi; \
	if [ -n "$$unformatted" ]; then echo "Files requiring gofmt:"; echo "$$unformatted"; exit 1; fi

check: vet fmt-check test

release-test:
	python3 -m unittest discover -s scripts -p 'test_*.py'

ci: build check release-test

fmt:
	gofmt -w cmd internal

snapshot:
	goreleaser release --snapshot --clean

release-check:
	goreleaser check
