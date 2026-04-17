SHELL := /usr/bin/env bash
GO    ?= go
PKG   := github.com/zaller/rollouts
BIN   := bin/rollouts-controller

CONTROLLER_GEN ?= $(shell $(GO) env GOPATH)/bin/controller-gen

.PHONY: all
all: build

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: generate
generate:
	./hack/update-codegen.sh

.PHONY: manifests
manifests:
	./hack/update-crds.sh

.PHONY: build
build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/rollouts-controller

.PHONY: test
test:
	$(GO) test ./...

.PHONY: envtest
envtest:
	$(GO) test ./test/envtest/...

.PHONY: kind-up
kind-up:
	kind create cluster --name rollouts

.PHONY: kind-e2e
kind-e2e:
	$(GO) test ./test/e2e/...

.PHONY: docker
docker:
	docker buildx build --platform linux/amd64,linux/arm64 -t rollouts-controller:dev .

.PHONY: clean
clean:
	rm -rf bin/
