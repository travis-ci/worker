SHELL := bash

PACKAGE_CHECKOUT := $(shell echo ${PWD})
PACKAGE := github.com/travis-ci/worker
ALL_PACKAGES := $(PACKAGE) $(shell script/list-packages) $(PACKAGE)/cmd/...

VERSION_VAR := $(PACKAGE).VersionString
VERSION_VALUE ?= $(shell git describe --always --dirty --tags 2>/dev/null)
REV_VAR := $(PACKAGE).RevisionString
REV_VALUE ?= $(shell git rev-parse HEAD 2>/dev/null || echo "'???'")
REV_URL_VAR := $(PACKAGE).RevisionURLString
REV_URL_VALUE ?= https://github.com/travis-ci/worker/tree/$(shell git rev-parse HEAD 2>/dev/null || echo "'???'")
GENERATED_VAR := $(PACKAGE).GeneratedString
GENERATED_VALUE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%S%z')
COPYRIGHT_VAR := $(PACKAGE).CopyrightString
COPYRIGHT_VALUE ?= $(shell grep -i ^copyright LICENSE | sed 's/^[Cc]opyright //')
DOCKER_IMAGE_REPO ?= travisci/worker
DOCKER_DEST ?= $(DOCKER_IMAGE_REPO):$(VERSION_VALUE)

DOCKER ?= docker
GO ?= go
GVT ?= gvt
GOPATH := $(shell echo $${GOPATH%%:*})
GOPATH_BIN := $(GOPATH)/bin
GOBUILD_LDFLAGS ?= \
	-extldflags '-static' \
	-X '$(VERSION_VAR)=$(VERSION_VALUE)' \
	-X '$(REV_VAR)=$(REV_VALUE)' \
	-X '$(REV_URL_VAR)=$(REV_URL_VALUE)' \
	-X '$(GENERATED_VAR)=$(GENERATED_VALUE)' \
	-X '$(COPYRIGHT_VAR)=$(COPYRIGHT_VALUE)'

export GO15VENDOREXPERIMENT
export DOCKER_DEST

COVERPROFILES := \
	root-coverage.coverprofile \
	backend-coverage.coverprofile \
	config-coverage.coverprofile \
	image-coverage.coverprofile
CROSSBUILD_BINARIES := \
	build/darwin/amd64/travis-worker \
	build/linux/amd64/travis-worker

SHFMT_URL := https://github.com/mvdan/sh/releases/download/v2.5.0/shfmt_v2.5.0_linux_amd64

%-coverage.coverprofile:
	$(GO) test -covermode=count -coverprofile=$@ \
		-tags netgo -ldflags "$(GOBUILD_LDFLAGS)" \
		$(PACKAGE)/$(subst -,/,$(subst root,,$(subst -coverage.coverprofile,,$@)))

.PHONY: %
%:
	./script/$@

.PHONY: all
all: clean test

.PHONY: test
test: vendor/.deps-fetched lintall build fmtpolice test-no-cover test-cover

.PHONY: test-cover
test-cover: coverage.html

.PHONY: test-no-cover
test-no-cover:
	$(GO) test -race -tags netgo -ldflags "$(GOBUILD_LDFLAGS)" $(ALL_PACKAGES)

coverage.html: coverage.coverprofile
	$(GO) tool cover -html=$^ -o $@

coverage.coverprofile: $(COVERPROFILES)
	./script/fold-coverprofiles $^ > $@
	$(GO) tool cover -func=$@

.PHONY: build
build: vendor/.deps-fetched
	$(GO) install -tags netgo -ldflags "$(GOBUILD_LDFLAGS)" $(ALL_PACKAGES)

.PHONY: crossbuild
crossbuild: vendor/.deps-fetched $(CROSSBUILD_BINARIES)

.PHONY: docker-build
docker-build:
	$(DOCKER) build -t $(DOCKER_DEST) .

build/darwin/amd64/travis-worker:
	GOARCH=amd64 GOOS=darwin CGO_ENABLED=0 \
		$(GO) build -o build/darwin/amd64/travis-worker \
		-ldflags "$(GOBUILD_LDFLAGS)" $(PACKAGE)/cmd/travis-worker

build/linux/amd64/travis-worker:
	GOARCH=amd64 GOOS=linux CGO_ENABLED=0 \
		$(GO) build -o build/linux/amd64/travis-worker \
		-ldflags "$(GOBUILD_LDFLAGS)" $(PACKAGE)/cmd/travis-worker

.PHONY: distclean
distclean: clean
	rm -rf vendor/.deps-fetched build/

.PHONY: deps
deps: .ensure-shfmt .ensure-gometalinter .ensure-gvt vendor/.deps-fetched

vendor/.deps-fetched: vendor/manifest
	$(GVT) rebuild
	touch $@

.PHONY: .ensure-shfmt
.ensure-shfmt:
	if ! shfmt -version 2>/dev/null; then \
		curl -o $(GOPATH_BIN)/shfmt -sSL $(SHFMT_URL); \
		chmod +x $(GOPATH_BIN)/shfmt; \
		shfmt -version; \
	fi

.PHONY: .ensure-gometalinter
.ensure-gometalinter:
	if ! command -v gometalinter &>/dev/null; then \
		go get -u github.com/alecthomas/gometalinter; \
		gometalinter --install; \
	fi

.PHONY: .ensure-gvt
.ensure-gvt:
	if ! command -v gvt &>/dev/null; then \
		go get -u github.com/FiloSottile/gvt; \
	fi

.PHONY: annotations
annotations:
	@git grep -E '(TODO|FIXME|XXX):' | grep -v -E 'Makefile|vendor/'

$(DOCKER_ENV_FILE):
	env | grep ^DOCKER | sort >$@
	echo 'DOCKER_DEST=$(DOCKER_DEST)' >>$@
