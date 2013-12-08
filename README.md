# Travis Worker

## Running Travis Worker

1. Copy `config/worker.json.example` to `config/worker.json` and update the details in it.
2. `go build`
3. `./travis-worker-go`

C-c will stop the worker. Note that any VMs for builds that were still running will have to be cleaned up manually.

## License and Copyright Information

See LICENSE file.

Copyright © 2013 Travis CI GmbH