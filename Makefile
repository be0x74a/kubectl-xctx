.PHONY: build test lint vuln clean

BINARY := kubectl-xctx

build:
	go build -o $(BINARY) .

test:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

clean:
	rm -f $(BINARY) coverage.out

.DEFAULT_GOAL := build
