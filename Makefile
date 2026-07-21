.PHONY: build check fmt fmt-check generate notices test tidy-check vet vuln

build:
	go build -trimpath -o nanit-controller ./cmd/nanit-controller

check: fmt-check tidy-check notices vet test
	go build ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || \
		(echo "gofmt required:"; gofmt -l cmd internal; exit 1)

generate:
	go generate ./internal/protocol

notices:
	./scripts/check-notices.sh

test:
	go test -race ./...

tidy-check:
	go mod tidy -diff

vet:
	go vet ./...

vuln:
	govulncheck ./...
