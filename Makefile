.PHONY: build generate test vet

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -o nanit-controller ./cmd/nanit-controller

generate:
	go generate ./internal/protocol
