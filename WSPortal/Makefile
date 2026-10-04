BINARY := ws
PKG := ./cmd/ws
VERSION ?= 0.1.0-dev
LDFLAGS := -X github.com/cheeselord1161/WS_Portal/internal/cli.Version=$(VERSION)
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test vet lint fmt fmt-check check clean run

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test:
	go test ./...

# Run the race detector, as CI does.
test-race:
	go test -race ./...

vet:
	go vet ./...

# Requires golangci-lint v2: https://golangci-lint.run/welcome/install/
lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	gofmt -l -w .

# Fail when any file is not gofmt-formatted, without rewriting it.
fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run 'make fmt'"; exit 1; }

# Run every check CI runs.
check: fmt-check vet lint test

clean:
	rm -f $(BINARY)

run: build
	./$(BINARY) --help
