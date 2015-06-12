PACKAGE_CHECKOUT := $(shell echo ${PWD})
PACKAGE := github.com/travis-ci/worker
PACKAGE_SRC_DIR := src/$(PACKAGE)
SUBPACKAGES := \
	$(PACKAGE)/cmd/travis-worker \
	$(PACKAGE)/backend \
	$(PACKAGE)/context \
	$(PACKAGE)/metrics

VERSION_VAR := $(PACKAGE).VersionString
VERSION_VALUE ?= $(shell git describe --always --dirty --tags 2>/dev/null)
REV_VAR := $(PACKAGE).RevisionString
REV_VALUE ?= $(shell git rev-parse --sq HEAD 2>/dev/null || echo "'???'")
GENERATED_VAR := $(PACKAGE).GeneratedString
GENERATED_VALUE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%S%z')

FIND ?= find
GO ?= go
GOXC ?= goxc
GOPATH := $(PACKAGE_CHECKOUT):$(PACKAGE_CHECKOUT)/vendor:$(shell echo $${GOPATH%%:*})
GOBUILD_LDFLAGS ?= -ldflags "\
	-X $(VERSION_VAR) '$(VERSION_VALUE)' \
	-X $(REV_VAR) $(REV_VALUE) \
	-X $(GENERATED_VAR) '$(GENERATED_VALUE)' \
"
GOBUILD_FLAGS ?= -x
GOXC_BUILD_CONSTRAINTS ?= amd64 linux,amd64 darwin

PORT ?= 42151
export PORT

COVERPROFILES := \
	coverage.coverprofile \
	backend-coverage.coverprofile

%-coverage.coverprofile:
	$(GO) test -covermode=count -coverprofile=$@ \
		$(GOBUILD_LDFLAGS) \
		$(PACKAGE)/$(subst -,/,$(subst -coverage.coverprofile,,$@))

.PHONY: all
all: clean deps test lintall

.PHONY: buildpack
buildpack:
	@$(MAKE) build \
		GOBUILD_FLAGS= \
		REV_VALUE="'$(shell git log -1 --format='%H')'" \
		VERSION_VALUE=buildpack-$(STACK)-$(USER)-$(DYNO)

.PHONY: test
test: build fmtpolice test-deps coverage.html

.PHONY: test-deps
test-deps:
	gb test

.PHONY: test-no-cover
test-no-cover:
	$(GO) test $(GOBUILD_LDFLAGS) $(PACKAGE) $(SUBPACKAGES)

.PHONY: test-race
test-race:
	$(GO) test -race $(GOBUILD_LDFLAGS) $(PACKAGE) $(SUBPACKAGES)

coverage.html: coverage.coverprofile
	$(GO) tool cover -html=$^ -o $@

coverage.coverprofile: $(COVERPROFILES)
	./utils/fold-coverprofiles $^ > $@
	$(GO) tool cover -func=$@

.PHONY: build
build:
	gb build

.PHONY: crossbuild
crossbuild:
	$(GOXC) -bc='$(GOXC_BUILD_CONSTRAINTS)' -d=.build/ -pv=$(VERSION_VALUE)

.PHONY: deps
deps:
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

