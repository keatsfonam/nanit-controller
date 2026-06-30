.PHONY: test build vet

test:
	go test ./...

vet:
	go vet ./...

build:
	go build -o nanit-controller ./cmd/nanit-controller
