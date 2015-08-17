package worker

import (
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

// FileCanceller fulfills the Canceller interface for file-based queues
type FileCanceller struct {
	baseDir string
	ctx     gocontext.Context
}

// NewFileCanceller creates a *FileCanceller
func NewFileCanceller(ctx gocontext.Context, baseDir string) *FileCanceller {
	return &FileCanceller{
		baseDir: baseDir,
		ctx:     context.FromComponent(ctx, "canceller"),
	}
}

// Run is a no-op
func (c *FileCanceller) Run() {
	return
}

// Subscribe is a no-op
func (c *FileCanceller) Subscribe(id uint64, ch chan<- struct{}) error {
	return nil
}

// Unsubscribe is a no-op
func (c *FileCanceller) Unsubscribe(id uint64) {
	return
}
