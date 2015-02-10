package lib

import (
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

type stepRunScript struct{}

func (s *stepRunScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx, cancelCtx := context.WithCancel(state.Get("ctx").(context.Context))
	buildJob := state.Get("buildJob").(Job)

	instance := state.Get("instance").(backend.Instance)

	logWriter, err := buildJob.LogWriter(ctx)
	if err != nil {
		LoggerFromContext(ctx).WithField("err", err).Error("couldn't open a log writer")
		buildJob.Requeue()
		return multistep.ActionHalt
	}

	resultChan := make(chan struct {
		result backend.RunResult
		err    error
	}, 1)

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
	case r := <-resultChan:
		if r.err != nil {
			LoggerFromContext(ctx).WithField("err", r.err).WithField("completed", r.result.Completed).Error("couldn't run script")

			if !r.result.Completed {
				buildJob.Requeue()
			}

			return multistep.ActionHalt
		}

		state.Put("scriptResult", r.result)
		return multistep.ActionContinue
	case <-cancelChan:
		cancelCtx()

		logWriter := logWriter.(*LogWriter)

		_, err := logWriter.WriteAndClose([]byte("\n\nDone: Job Cancelled\n\n"))
		if err != nil {
			LoggerFromContext(ctx).WithField("err", err).Error("couldn't write cancellation log message")
		}

		err = buildJob.Finish(FinishStateCancelled)
		if err != nil {
			LoggerFromContext(ctx).WithField("err", err).Error("couldn't update job state to cancelled")
		}

		return multistep.ActionHalt
	}
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
