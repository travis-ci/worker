package lib

import (
	"time"

	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"github.com/travis-ci/worker/lib/context"
	gocontext "golang.org/x/net/context"
)

type stepRunScript struct {
	logTimeout   time.Duration
	maxLogLength int
}

func (s *stepRunScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx, cancelCtx := gocontext.WithCancel(state.Get("ctx").(gocontext.Context))
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)

	logWriter, err := buildJob.LogWriter(ctx)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't open a log writer")
		buildJob.Requeue()
		return multistep.ActionHalt
	}

	logWriter.SetTimeout(s.logTimeout)
	logWriter.SetMaxLogLength(s.maxLogLength)

	resultChan := make(chan struct {
		result backend.RunResult
		err    error
	}, 1)

	context.LoggerFromContext(ctx).Info("running script")
	defer context.LoggerFromContext(ctx).Info("finished script")

	go func() {
		result, err := instance.RunScript(ctx, logWriter)
		resultChan <- struct {
			result backend.RunResult
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
				buildJob.Requeue()
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

		_, err := logWriter.WriteAndClose([]byte("\n\nNo output has been received in the last ... minutes, this potentially indicates a stalled build or something wrong with the build itself.\n\nThe build has been terminated\n\n"))
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write log timeout log message")
		}

		err = buildJob.Finish(FinishStateErrored)
		if err != nil {
			context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't update job state to errored")
		}

		return multistep.ActionHalt
	}
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
