# Travis Worker

## Installing Travis Worker

1. Make sure you have [Go](http://golang.org) installed, and your
   [GOPATH](http://golang.org/doc/code.html#GOPATH) set up.
2. Clone the repository to somewhere in your GOPATH (usually
   `$GOPATH/src/github.com/henrikhodne/travis-worker-go`), unless you have more
   than one directory in your GOPATH.
3. `go get .` inside this directory to download dependencies.
4. Copy `config/worker.json.example` to `config/worker.json` and update the
   details inside of it.

## Running Travis Worker

1. `go build`
2. `./travis-worker-go`

C-c will stop the worker. Note that any VMs for builds that were still running
will have to be cleaned up manually.

## License and Copyright Information

See LICENSE file.

Copyright © 2013 Travis CI GmbH