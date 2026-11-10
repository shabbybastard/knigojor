.PHONY: all build test vet package clean

# An exact tag wins; otherwise the version from VERSION is used.
TAG_VERSION := $(shell git describe --tags --exact-match 2>/dev/null | sed 's/^v//')
VERSION ?= $(if $(TAG_VERSION),$(TAG_VERSION),$(shell cat VERSION 2>/dev/null || git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo dev))
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

package:
	mkdir -p bin dist
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o bin/knigojor ./cmd/knigojor; \
		VERSION=$(VERSION) PKG_ARCH=$$arch go run github.com/goreleaser/nfpm/v2/cmd/nfpm@latest package -f packaging/nfpm.yaml -p deb -t dist/; \
		VERSION=$(VERSION) PKG_ARCH=$$arch go run github.com/goreleaser/nfpm/v2/cmd/nfpm@latest package -f packaging/nfpm.yaml -p rpm -t dist/; \
	done

clean:
	rm -rf bin dist
