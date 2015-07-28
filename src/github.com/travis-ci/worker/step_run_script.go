package worker

import (
	"fmt"
	"time"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

type stepRunScript struct {
	logTimeout               time.Duration
	hardTimeout              time.Duration
	skipShutdownOnLogTimeout bool
	maxLogLength             int
}

func (s *stepRunScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx, cancelCtx := gocontext.WithCancel(state.Get("ctx").(gocontext.Context))
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)

	logWriter, err := buildJob.LogWriter(ctx)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't open a log writer")
		err := buildJob.Requeue()
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
		}
		return multistep.ActionHalt
	}
	defer logWriter.Close()

	logWriter.SetTimeout(s.logTimeout)
	logWriter.SetMaxLogLength(s.maxLogLength)

	resultChan := make(chan struct {
		result *backend.RunResult
		err    error
	}, 1)

	context.LoggerFromContext(ctx).Info("running script")
	defer context.LoggerFromContext(ctx).Info("finished script")

	go func() {
		if hostname, ok := state.Get("hostname").(string); ok && hostname != "" {
			_, _ = logWriter.Write([]byte(fmt.Sprintf("Using worker: %s (%s)\n\n", hostname, instance.ID())))
		}
		result, err := instance.RunScript(ctx, logWriter)
		resultChan <- struct {
			result *backend.RunResult
			err    error
		}{
			result: result,
			err:    err,
		}
	}()

	cancelChan := state.Get("cancelChan").(<-chan struct{})

	select {
	// This needs to be before <-resultChan, since cancelling the context is
	// likely going to cause resultChan to get something sent to it if the
	// script stops fast enough.
	case <-ctx.Done():
		cancelCtx()

		if ctx.Err() == gocontext.DeadlineExceeded {
			context.LoggerFromContext(ctx).Info("hard timeout exceeded, terminating")
			_, err := logWriter.WriteAndClose([]byte("\n\nThe job exceeded the maxmimum time limit for jobs, and has been terminated.\n\n"))
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write hard timeout log message")
			}

			err = buildJob.Finish(FinishStateErrored)
			if err != nil {
				context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't update job state to errored")
			}
		} else {
			context.LoggerFromContext(ctx).Info("context was cancelled, stopping job")
		}

		return multistep.ActionHalt
	case r := <-resultChan:
		if r.err != nil {
			context.LoggerFromContext(ctx).WithField("err", r.err).WithField("completed", r.result.Completed).Error("couldn't run script")

			if !r.result.Completed {
				err := buildJob.Requeue()
				if err != nil {
					context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
				}
			}

			return multistep.ActionHalt
		}

		state.Put("scriptResult", r.result)
		return multistep.ActionContinue
	case <-cancelChan:
		cancelCtx()

		_, err := logWriter.WriteAndClose([]byte("\n\nDone: Job Cancelled\n\n"))
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write cancellation log message")
		}

		err = buildJob.Finish(FinishStateCancelled)
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't update job state to cancelled")
		}

		return multistep.ActionHalt
	case <-logWriter.Timeout():
		cancelCtx()

		_, err := logWriter.WriteAndClose([]byte(fmt.Sprintf("\n\nNo output has been received in the last %v, this potentially indicates a stalled build or something wrong with the build itself.\n\nThe build has been terminated\n\n", s.logTimeout)))
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write log timeout log message")
		}

		err = buildJob.Finish(FinishStateErrored)
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't update job state to errored")
		}

		if s.skipShutdownOnLogTimeout {
			state.Put("skipShutdown", true)
		}

		return multistep.ActionHalt
	}
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
