.PHONY: build test clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/nvidia-exporter ./cmd/nvidia-exporter

test:
	go test -race ./...

clean:
	rm -rf bin/
