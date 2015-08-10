# Travis Worker

## Installing Travis Worker

### from binary

0. download the [64-bit linux
   binary](https://travis-worker-artifacts.s3.amazonaws.com/travis-ci/worker/v0.4.3/build/linux/amd64/travis-worker)
0. put it somewhere in `$PATH`, e.g. `/usr/local/bin/travis-worker`

### from package

*coming soon!*

### from source
0. clone this down
0. install [Go](http://golang.org) and [gb](https://github.com/constabulary/gb).
0. `make`

## Configuring Travis Worker

Travis Worker is configured with environment variables or command line flags via
the [codegangsta/cli](https://github.com/codegangsta/cli) library.  A list of
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
0. `./bin/travis-worker`

C-c will stop the worker. Note that any VMs for builds that were still running
will have to be cleaned up manually.

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

Travis Worker is built using [`gb`](http://getgb.io) and dependencies
are vendored into this repository. They are managed with
[`gb-vendor`](http://godoc.org/github.com/constabulary/gb/cmd/gb-vendor).

To work with the dependencies you need to do the following first

- Have this repository checked out
- Install `gb` with `go get github.com/constabulary/gb/...`

### Updating existing vendored dependencies

To update and existing vendored dependency, do the following:

1. `cd ~/code/travis-worker`
2. `gb vendor update name/of/package` e.g. `gb vendor update github.com/pkg/sftp`

To update all the dependencies, do the following:

1. `cd ~/code/travis-worker`
2. `gb vendor update --all`

### Adding a new dependency

To add a new dependency, do the following:

1. `cd ~/code/travis-worker`
2. `gb vendor fetch name/of/package` e.g. `gb vendor fetch github.com/pkg/sftp`

## License and Copyright Information

See LICENSE file.

© 2014-2015 Travis CI GmbH
