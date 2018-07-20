package worker

import (
	gocontext "context"
	"time"
)

type LogsQueue interface {
	LogWriter(gocontext.Context, time.Duration, Job) (LogWriter, error)
	Cleanup() error
}
