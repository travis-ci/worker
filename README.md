# Worker [![Build Status](https://travis-ci.org/travis-ci/worker.svg?branch=master)](https://travis-ci.org/travis-ci/worker)

Worker is the component of Travis CI that will run a CI job on some form of
compute instance. It's responsible for getting the bash script from
[travis-build](https://github.com/travis-ci/travis-build), spin up the compute
instance (VM, Docker container, or maybe something different), upload the bash
script, run it and stream the logs back to
[travis-logs](https://github.com/travis-ci/travis-logs). It also sends state
updates to [travis-hub](https://github.com/travis-ci/travis-hub).

## Installing

### from binary

Find the version you wish to install on the [GitHub Releases
page](https://github.com/travis-ci/worker/releases) and download either the
`darwin-amd64` binary for OS X or the `linux-amd64` binary for Linux. No other
operating systems or architectures have pre-built binaries at this time.

### from package

Use the [`./bin/travis-worker-install`](./bin/travis-worker-install) script,
or take a look at the [packagecloud
instructions](https://packagecloud.io/travisci/worker/install).

### from source

1. install [Go](http://golang.org) `v1.7+`
1. clone this down into your `$GOPATH`
  * `mkdir -p $GOPATH/src/github.com/travis-ci`
  * `git clone https://github.com/travis-ci/worker $GOPATH/src/github.com/travis-ci/worker`
  * `cd $GOPATH/src/github.com/travis-ci/worker`
1. install [gvt](https://github.com/FiloSottile/gvt):
  * `go get -u github.com/FiloSottile/gvt`
1. install [gometalinter](https://github.com/alecthomas/gometalinter):
  * `go get -u github.com/alecthomas/gometalinter`
  * `gometalinter --install`
1. `make`

## Configuring Travis Worker

Travis Worker is configured with environment variables or command line flags via
the [urfave/cli](https://github.com/urfave/cli) library.  A list of
the non-dynamic flags and environment variables may be found by invoking the
built-in help system:

``` bash
travis-worker --help
```

### Configuring the requested provider

Each provider requires its own configuration, which must be provided via
environment variables namespaced by `TRAVIS_WORKER_{PROVIDER}_`, e.g. for the
docker provider:

``` bash
export TRAVIS_WORKER_DOCKER_ENDPOINT="tcp://localhost:4243"
export TRAVIS_WORKER_DOCKER_PRIVILEGED="false"
export TRAVIS_WORKER_DOCKER_CERT_PATH="/etc/secret-docker-cert-stuff"
```

### Verifying and exporting configuration

To inspect the parsed configuration in a format that can be used as a base
environment variable configuration, use the `--echo-config` flag, which will
exit immediately after writing to stdout:

``` bash
travis-worker --echo-config
```

## Running Travis Worker

0. `make`
0. `${GOPATH%%:*}/bin/travis-worker`

## Stopping Travis Worker

Travis Worker has two shutdown modes: Graceful and immediate. The graceful
shutdown will tell the worker to not start any additional jobs, but finish the
jobs it is currently running before it shuts down. The immediate shutdown will
make the worker stop the jobs it's working on and requeue them, and clean up any
open resources (shut down VMs, cleanly close connections, etc.)

To start a graceful shutdown, send an INT signal to the worker (for example
using `kill -INT`). To start an immediate shutdown, send a TERM signal to the
worker (for example using `kill -TERM`).

## Go dependency management

Travis Worker is built via the standard `go` commands, and dependencies managed
by [`gvt`](https://github.com/FiloSottile/gvt).

To work with the dependencies you need to do the following first

- Have this repository checked out
- Install `gvt` with `github.com/FiloSottile/gvt`

### Updating existing vendored dependencies

To update and existing vendored dependency, do the following in *this directory*:

- `gvt update name/of/dependency` e.g. `gvt update github.com/pkg/sftp`

### Adding a new dependency

To add a new dependency, do the following:

- `gvt fetch name/of/package` e.g. `gvt fetch github.com/pkg/sftp`

## Development

This section is for anyone wishing to contribute code to Worker. The code
itself _should_ have godoc-compatible docs (which can be viewed on godoc.org:
<https://godoc.org/github.com/travis-ci/worker>), this is mainly a higher-level
overview of the code.

## Release process

The parts of the release process that haven't yet been automated look like this:

- [ ] review the diff since last release for silliness
- [ ] decide what the version bump should be
- [ ] update [`./CHANGELOG.md`](./CHANGELOG.md) (in a release prep branch)
- [ ] tag accordingly after merge
- [ ] update github release tag with relevant section from [`./CHANGELOG.md`](./CHANGELOG.md)
- [ ] attach binaries to github release tag

## License and Copyright Information

See [LICENSE file](./LICENSE).

© 2017 Travis CI GmbH
