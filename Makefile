.PHONY: build test check
build:
	CGO_ENABLED=0 go build -trimpath -o bin/cosense-simple-mcp ./cmd/cosense-simple-mcp

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...
