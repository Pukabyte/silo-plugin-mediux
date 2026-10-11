.PHONY: build test vet check audit dist
BINARY=plugin
VERSION ?= 0.1.7
GO ?= go
GOFMT ?= $(shell $(GO) env GOROOT)/bin/gofmt
build:
	$(GO) build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o $(BINARY) .
test:
	$(GO) test -race ./...
vet:
	$(GO) vet ./...
check: test vet
	test -z "$$("$(GOFMT)" -l .)"
	$(GO) mod verify
	python3 -m unittest discover -s tests -p 'test_*.py'
audit:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
dist:
	GO=$(GO) python3 tools/package.py --version $(VERSION)
