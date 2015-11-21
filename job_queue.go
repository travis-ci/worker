package worker

import (
	gocontext "golang.org/x/net/context"
)

// JobQueue is the minimal interface needed by a ProcessorPool
type JobQueue interface {
	Jobs(gocontext.Context) (<-chan Job, error)
	Cleanup() error
}
