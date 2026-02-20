.PHONY: build test lint vuln clean install uninstall

BINARY := kubectl-xctx
COMPLETION := kubectl_complete-xctx
PREFIX ?= /usr/local/bin

build:
	go build -o $(BINARY) .

test:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint:
	golangci-lint run ./...

vuln:
	govulncheck ./...

install: build
	install -m 755 $(BINARY) $(PREFIX)/$(BINARY)
	install -m 755 $(COMPLETION) $(PREFIX)/$(COMPLETION)

uninstall:
	rm -f $(PREFIX)/$(BINARY) $(PREFIX)/$(COMPLETION)

clean:
	rm -f $(BINARY) coverage.out

.DEFAULT_GOAL := build
