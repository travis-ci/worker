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

GO ?= go
GOXC ?= goxc
GVT ?= gvt
GOPATH := $(shell echo $${GOPATH%%:*})
GOBUILD_LDFLAGS ?= \
	-X '$(VERSION_VAR)=$(VERSION_VALUE)' \
	-X '$(REV_VAR)=$(REV_VALUE)' \
	-X '$(REV_URL_VAR)=$(REV_URL_VALUE)' \
	-X '$(GENERATED_VAR)=$(GENERATED_VALUE)' \
	-X '$(COPYRIGHT_VAR)=$(COPYRIGHT_VALUE)'

export GO15VENDOREXPERIMENT

COVERPROFILES := \
	backend-coverage.coverprofile \
	config-coverage.coverprofile \
	context-coverage.coverprofile \
	image-coverage.coverprofile \
	metrics-coverage.coverprofile

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
crossbuild: .crossdeps deps
	$(GOXC) -pv=$(VERSION_VALUE) \
		-build-ldflags "$(GOBUILD_LDFLAGS) -s -w" \
		xc
	$(GOXC) -pv=$(VERSION_VALUE) \
		-build-ldflags "$(GOBUILD_LDFLAGS)" \
		-o "{{.Dest}}{{.PS}}{{.Os}}{{.PS}}{{.Arch}}{{.PS}}travis-worker-dbg{{.Ext}}" \
		xc

.crossdeps:
	GOROOT_BOOTSTRAP=$(GOROOT) $(GOXC) -t
	touch $@

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
