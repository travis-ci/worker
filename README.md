# Travis Worker

## Installing Travis Worker

1. Install [Go](http://golang.org) and [Godep](https://github.com/kr/godep).
2. Copy `config/worker.json.example` to `config/worker.json` and update the
   details inside of it.

## Running Travis Worker

1. `godep go build`
2. `./travis-worker-go`

C-c will stop the worker. Note that any VMs for builds that were still running
will have to be cleaned up manually.

## License and Copyright Information

See LICENSE file.

Copyright © 2013 Travis CI GmbH