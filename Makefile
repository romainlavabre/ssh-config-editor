# Go n'est pas requis sur le poste : tout passe par l'image officielle.
GO_IMAGE ?= golang:1.25
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

DOCKER := docker run --rm \
	-u $(shell id -u):$(shell id -g) -v /etc/passwd:/etc/passwd:ro -v /etc/group:/etc/group:ro \
	-e HOME=/tmp -e GOCACHE=/src/.cache/build -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 \
	-v $(CURDIR):/src -w /src $(GO_IMAGE)

.PHONY: build dist test vet fmt tidy clean

build: ## binaire linux/amd64 dans dist/
	$(DOCKER) go build -trimpath -ldflags '$(LDFLAGS)' -o dist/ssh-config-editor ./cmd/ssh-config-editor

dist: ## binaires pour linux amd64/arm64 et macOS arm64
	$(DOCKER) sh -c '\
	  GOOS=linux  GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ssh-config-editor-linux-amd64  ./cmd/ssh-config-editor && \
	  GOOS=linux  GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ssh-config-editor-linux-arm64  ./cmd/ssh-config-editor && \
	  GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/ssh-config-editor-darwin-arm64 ./cmd/ssh-config-editor'

test:
	$(DOCKER) go test $(TESTFLAGS) ./...

vet:
	$(DOCKER) go vet ./...

fmt:
	$(DOCKER) gofmt -l -w cmd internal

tidy:
	$(DOCKER) go mod tidy

clean:
	rm -rf dist .cache
