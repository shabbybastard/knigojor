.PHONY: all build test vet clean

# Local builds use VERSION; release builds pass the tag explicitly.
VERSION ?= $(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev)
override VERSION := $(patsubst v%,%,$(VERSION))

LDFLAGS := -s -w -X main.version=$(VERSION)

all: build

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/knigojor ./cmd/knigojor

test:
	go test -race ./...

vet:
	go vet ./...

clean:
	rm -rf bin dist
