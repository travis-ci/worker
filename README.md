# Travis Worker

## Installing Travis Worker

1. Install [Go](http://golang.org) and
   [Deppy](https://github.com/hamfist/deppy).

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

0. `deppy restore`
0. `make`
0. `${GOPATH%%:*}/bin/travis-worker`

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

## License and Copyright Information

See LICENSE file.

Copyright © 2014 Travis CI GmbH
