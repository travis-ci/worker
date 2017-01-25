PACKAGE_CHECKOUT := $(shell echo ${PWD})
PACKAGE := github.com/travis-ci/worker
ALL_PACKAGES := $(shell utils/list-packages) $(PACKAGE)/cmd/...

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
	backend-coverage.coverprofile \
	config-coverage.coverprofile \
	context-coverage.coverprofile \
	image-coverage.coverprofile \
	metrics-coverage.coverprofile
CROSSBUILD_BINARIES := \
	build/darwin/amd64/travis-worker \
	build/linux/amd64/travis-worker

%-coverage.coverprofile:
	$(GO) test -v -covermode=count -coverprofile=$@ \
		-x -ldflags "$(GOBUILD_LDFLAGS)" \
		$(PACKAGE)/$(subst -,/,$(subst -coverage.coverprofile,,$@))

.PHONY: %
%:
	./utils/$@

.PHONY: all
all: clean test

.PHONY: test
test: deps lintall build fmtpolice test-no-cover coverage.html

.PHONY: test-no-cover
test-no-cover:
	$(GO) test -v -x -ldflags "$(GOBUILD_LDFLAGS)" $(ALL_PACKAGES)

.PHONY: test-race
test-race: deps
	$(GO) test -v -race -x -ldflags "$(GOBUILD_LDFLAGS)" $(ALL_PACKAGES)

coverage.html: coverage.coverprofile
	$(GO) tool cover -html=$^ -o $@

coverage.coverprofile: $(COVERPROFILES)
	./utils/fold-coverprofiles $^ > $@
	$(GO) tool cover -func=$@

.PHONY: build
build: deps
	$(GO) install -x -ldflags "$(GOBUILD_LDFLAGS)" $(ALL_PACKAGES)

.PHONY: crossbuild
crossbuild: deps $(CROSSBUILD_BINARIES)

.PHONY: docker-build
docker-build: $(CROSSBUILD_BINARIES)
	$(DOCKER) build -t $(DOCKER_DEST) .

$(CROSSBUILD_BINARIES):
	GOARCH=amd64 GOOS=darwin CGO_ENABLED=0 \
		$(GO) build -o build/darwin/amd64/travis-worker \
		-ldflags "$(GOBUILD_LDFLAGS)" $(PACKAGE)/cmd/travis-worker
	GOARCH=amd64 GOOS=linux CGO_ENABLED=0 \
		$(GO) build -o build/linux/amd64/travis-worker \
		-ldflags "$(GOBUILD_LDFLAGS)" $(PACKAGE)/cmd/travis-worker

.PHONY: distclean
distclean: clean
	rm -f vendor/.deps-fetched

.PHONY: deps
deps: vendor/.deps-fetched

vendor/.deps-fetched:
	$(GVT) rebuild
	touch $@

.PHONY: annotations
annotations:
	@git grep -E '(TODO|FIXME|XXX):' | grep -v -E 'Makefile|vendor/'

$(DOCKER_ENV_FILE):
	env | grep ^DOCKER | sort >$@
	echo 'DOCKER_DEST=$(DOCKER_DEST)' >>$@
