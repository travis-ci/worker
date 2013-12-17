# Travis CI Mac VM API Client

gotravismacapi is a library for interacting with the API that Travis CI uses to
spin up Mac VMs.

The API is private, so this library is unlikely to be very useful to you as-is,
but it is made available in the hope that it will still be useful to someone.

## Installation

Standard `go get`:

    $ go get github.com/henrikhodne/gotravismacapi

## Usage & Example

For usage and examples, see the
[Godoc](http://godoc.org/github.com/henrikhodne/gotravismacapi)

## Contributions & Issues

Contributions are welcome. Please run `go vet`, `go fmt` and
[`golint`](https://github.com/golang/lint) first (I will before merging, so the
PR will probably be merged faster if no issues show up). The issues raised by
these tools don't necessarily mean that anything is wrong, but they are often an
indication that something might be.

## License

Licensed under the MIT License. See the LICENSE file for details.
