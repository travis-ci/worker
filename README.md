# Travis Worker

## Installing Travis Worker

1. Install [Go](http://golang.org) and [Deppy](https://github.com/hamfist/deppy).
2. Copy `config/worker.json.example` to `config/worker.json` and update the
   details inside of it.

## Running Travis Worker

0. `deppy restore`
0. `make`
0. `${GOPATH%%:*}/bin/travis-worker`

C-c will stop the worker. Note that any VMs for builds that were still running
will have to be cleaned up manually.

## Running integration tests

The integration tests are in the `test/` directory and are written in Haskell. Currently running them isn't quite as automatic as I'd like, but you can do this:

1. Compile travis-worker with `make`
2. Run an instance of the worker with a fake provider with this command: `AMQP_URI=amqp:// POOL_SIZE=1 BUILD_API_URI=http://localhost:3000/script PROVIDER_NAME=fake PROVIDER_CONFIG=happy QUEUE_NAME=builds.test travis-worker`
3. First time after cloning: `cd test/ && cabal sandbox init`
4. To compile and run tests: `cd test/ && cabal run`

You need Haskell installed to get this to work. [Haskell Platform](https://www.haskell.org/platform/) is probably your best bet.

## License and Copyright Information

See LICENSE file.

Copyright © 2014 Travis CI GmbH
