.PHONY: build clean install test run

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

build:
	go build $(LDFLAGS) -o overseer ./cmd/overseer

install:
	go install $(LDFLAGS) ./cmd/overseer

clean:
	rm -f overseer
	go clean

test:
	go test -v ./...

run: build
	./overseer daemon

# Development helpers
init: build
	./overseer init

lint:
	golangci-lint run

fmt:
	go fmt ./...
	goimports -w .
