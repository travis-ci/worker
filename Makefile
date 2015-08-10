PACKAGE_CHECKOUT := $(shell echo ${PWD})
PACKAGE := github.com/travis-ci/worker
PACKAGE_SRC_DIR := src/$(PACKAGE)
ALL_PACKAGES := $(shell find src/github.com/travis-ci/worker -type d | sed 's@src@@;s@^/@@')

VERSION_VAR := $(PACKAGE).VersionString
VERSION_VALUE ?= $(shell git describe --always --dirty --tags 2>/dev/null)
REV_VAR := $(PACKAGE).RevisionString
REV_VALUE ?= $(shell git rev-parse --sq HEAD 2>/dev/null || echo "'???'")
GENERATED_VAR := $(PACKAGE).GeneratedString
GENERATED_VALUE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%S%z')
COPYRIGHT_VAR := $(PACKAGE).CopyrightString
COPYRIGHT_VALUE ?= $(shell grep -i ^copyright LICENSE | sed 's/^[Cc]opyright //')

FIND ?= find
GO ?= go
GB ?= gb
GOXC ?= goxc
GOPATH := $(PACKAGE_CHECKOUT):$(PACKAGE_CHECKOUT)/vendor:$(shell echo $${GOPATH%%:*})
GOBUILD_LDFLAGS ?= -ldflags "\
	-X $(VERSION_VAR) '$(VERSION_VALUE)' \
	-X $(REV_VAR) $(REV_VALUE) \
	-X $(GENERATED_VAR) '$(GENERATED_VALUE)' \
	-X $(COPYRIGHT_VAR) '$(COPYRIGHT_VALUE)' \
"
GOBUILD_FLAGS ?= -x
GOXC_BUILD_CONSTRAINTS ?= amd64 linux,amd64 darwin

PORT ?= 42151
export PORT

COVERPROFILES := \
	backend-coverage.coverprofile \
	metrics-coverage.coverprofile \
	config-coverage.coverprofile \
	context-coverage.coverprofile

%-coverage.coverprofile:
	$(GO) test -v -covermode=count -coverprofile=$@ \
		$(GOBUILD_LDFLAGS) \
		$(PACKAGE)/$(subst -,/,$(subst -coverage.coverprofile,,$@))

.PHONY: all
all: clean lintall test

.PHONY: buildpack
buildpack:
	@$(MAKE) build \
		GOBUILD_FLAGS= \
		REV_VALUE="'$(shell git log -1 --format='%H')'" \
		VERSION_VALUE=buildpack-$(STACK)-$(USER)-$(DYNO)

.PHONY: test
test: build fmtpolice .test coverage.html

.PHONY: .test
.test:
	$(GB) test -v

.PHONY: test-no-cover
test-no-cover:
	$(GO) test -v $(GOBUILD_LDFLAGS) $(ALL_PACKAGES)

.PHONY: test-race
test-race:
	$(GO) test -v -race $(GOBUILD_LDFLAGS) $(ALL_PACKAGES)

coverage.html: coverage.coverprofile
	$(GO) tool cover -html=$^ -o $@

coverage.coverprofile: $(COVERPROFILES)
	./utils/fold-coverprofiles $^ > $@
	$(GO) tool cover -func=$@

.PHONY: build
build:
	gb build $(GOBUILD_LDFLAGS)

.PHONY: crossbuild
crossbuild:
	$(GOXC) -bc='$(GOXC_BUILD_CONSTRAINTS)' -d=.build/ -pv=$(VERSION_VALUE)

.PHONY: update
update:
	gb vendor update --all

.PHONY: clean
clean:
	./utils/clean

.PHONY: annotations
annotations:
	@git grep -E '(TODO|FIXME|XXX):' | grep -v Makefile

.PHONY: fmtpolice
fmtpolice:
	./utils/fmtpolice $(PACKAGE_SRC_DIR)

.PHONY: lintall
lintall:
	./utils/lintall

.PHONY:  package
package:
	./utils/pkg/pkg_run
