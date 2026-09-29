# Go n'est pas requis sur le poste : tout passe par l'image officielle.
GO_IMAGE ?= golang:1.25
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

DOCKER := docker run --rm \
	-u $(shell id -u):$(shell id -g) -v /etc/passwd:/etc/passwd:ro -v /etc/group:/etc/group:ro \
	-e HOME=/tmp -e GOCACHE=/src/.cache/build -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 \
	-v $(CURDIR):/src -w /src $(GO_IMAGE)

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build dist release publish test vet fmt tidy clean

build: ## binaire linux/amd64 dans dist/
	$(DOCKER) go build -trimpath -ldflags '$(LDFLAGS)' -o dist/ssh-config-editor ./cmd/ssh-config-editor

dist: ## un binaire par plateforme de PLATFORMS
	$(DOCKER) sh -c 'set -e; for p in $(PLATFORMS); do \
	  GOOS=$${p%/*} GOARCH=$${p#*/} go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o dist/ssh-config-editor-$${p%/*}-$${p#*/} ./cmd/ssh-config-editor; done'

release: dist ## binaires + checksums.txt, prêts à attacher à une release GitHub
	$(DOCKER) sh -c 'cd dist && sha256sum ssh-config-editor-*-* > checksums.txt'

publish: release ## crée la release GitHub $(VERSION) (le tag doit exister et être poussé)
	gh release create $(VERSION) dist/ssh-config-editor-*-* dist/checksums.txt install.sh \
	  --title "ssh-config-editor $(VERSION)" --generate-notes

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
