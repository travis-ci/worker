# Travis Worker

## Installing Travis Worker

1. Install [Go](http://golang.org) and [Deppy](https://github.com/hamfist/deppy).
2. Copy `config/worker.json.example` to `config/worker.json` and update the
   details inside of it.

## Configuring Travis Worker

Travis Worker is configured with environment variables. Here is a list of all environment variables in use by Travis Worker:

- `AMQP_URI`: The URI to the AMQP server to connect to.
- `POOL_SIZE`: The size of the processor pool, affecting the number of jobs this worker can run in parallel.
- `BUILD_API_URI`: The full URL to the build API endpoint to use. Note that this also requires the path of the URL. If a username is included in the URL, this will be translated to a token passed in the Authorization header.
- `PROVIDER_NAME`: The name of the provider to use. See below for provider-specific configuration.
- `QUEUE_NAME`: The AMQP queue to subscribe to for jobs.
- `LIBRATO_EMAIL`, `LIBRATO_TOKEN`, `LIBRATO_SOURCE`: Librato metrics configuration. If not all are set, metrics will be printed to stderr.
- `SENTRY_DSN`: The DSN to send Sentry events to.

### Configuring the Sauce Labs provider

The Sauce Labs provider (used when `PROVIDER_NAME=sauce_labs`) needs a few more configuration settings:

- `TRAVIS_WORKER_SAUCE_LABS_ENDPOINT`: The endpoint to the Sauce Labs API.
- `TRAVIS_WORKER_SAUCE_LABS_IMAGE_ALIASES`: A comma-separated list of image aliase names to define. This should at least contain "default". For each alias `name` there should also be a variable named `TRAVIS_WORKER_SAUCE_LABS_IMAGE_ALIAS_NAME` containing the name of the image to boot when the `name` alias is requested.
- `TRAVIS_WORKER_SAUCE_LABS_SSH_KEY_PATH`: The path to the SSH key used to SSH into the VMs.
- `TRAVIS_WORKER_SAUCE_LABS_SSH_KEY_PASSPHRASE`: The passphrase to the SSH key used to SSH into the VMs.

## Running Travis Worker

0. `deppy restore`
0. `make`
0. `${GOPATH%%:*}/bin/travis-worker`

C-c will stop the worker. Note that any VMs for builds that were still running
will have to be cleaned up manually.

## Stopping Travis Worker

Travis Worker has two shutdown modes: Graceful and immediate. The graceful shutdown will tell the worker to not start any additional jobs, but finish the jobs it is currently running before it shuts down. The immediate shutdown will make the worker stop the jobs it's working on and requeue them, and clean up any open resources (shut down VMs, cleanly close connections, etc.)

To start a graceful shutdown, send an INT signal to the worker (for example using `kill -INT`). To start an immediate shutdown, send a TERM signal to the worker (for example using `kill -TERM`).

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
