package worker

import (
	gocontext "context"
)

type LogsQueue interface {
	LogWriter(gocontext.Context) (LogWriter, error)
	Cleanup() error
}
