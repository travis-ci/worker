package worker

import (
	"fmt"
	"time"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	gocontext "golang.org/x/net/context"
)

type runScriptReturn struct {
	result *backend.RunResult
	err    error
}

type stepRunScript struct {
	logTimeout               time.Duration
	hardTimeout              time.Duration
	skipShutdownOnLogTimeout bool
}

func (s *stepRunScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	instance := state.Get("instance").(backend.Instance)
	logWriter := state.Get("logWriter").(LogWriter)
	cancelChan := state.Get("cancelChan").(<-chan struct{})

	context.LoggerFromContext(ctx).Info("running script")
	defer context.LoggerFromContext(ctx).Info("finished script")

	resultChan := make(chan runScriptReturn, 1)
	go func() {
		result, err := instance.RunScript(ctx, logWriter)
		resultChan <- runScriptReturn{
			result: result,
			err:    err,
		}
	}()

	select {
	case r := <-resultChan:
		if errors.Cause(r.err) == ErrWrotePastMaxLogLength {
			context.LoggerFromContext(ctx).Info("wrote past maximum log length")
			s.writeLogAndFinishWithState(ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum log length, and has been terminated.\n\n")
			return multistep.ActionHalt
		}

		// We need to check for this since it's possible that the RunScript
		// implementation returns with the error too quickly for the ctx.Done()
		// case branch below to catch it.
		if errors.Cause(r.err) == gocontext.DeadlineExceeded {
			context.LoggerFromContext(ctx).Info("hard timeout exceeded, terminating")
			s.writeLogAndFinishWithState(ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum time limit for jobs, and has been terminated.\n\n")
			return multistep.ActionHalt
		}

		if r.err != nil {
			if !r.result.Completed {
				context.LoggerFromContext(ctx).WithField("err", r.err).WithField("completed", r.result.Completed).Error("couldn't run script, attempting requeue")
				context.CaptureError(ctx, r.err)

				err := buildJob.Requeue()
				if err != nil {
					context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't requeue job")
				}
			} else {
				context.LoggerFromContext(ctx).WithField("err", r.err).WithField("completed", r.result.Completed).Error("couldn't run script")
			}

			return multistep.ActionHalt
		}

		state.Put("scriptResult", r.result)

		return multistep.ActionContinue
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			context.LoggerFromContext(ctx).Info("hard timeout exceeded, terminating")
			s.writeLogAndFinishWithState(ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum time limit for jobs, and has been terminated.\n\n")
			return multistep.ActionHalt
		} else {
			context.LoggerFromContext(ctx).Info("context was cancelled, stopping job")
		}

		return multistep.ActionHalt
	case <-cancelChan:
		s.writeLogAndFinishWithState(ctx, logWriter, buildJob, FinishStateCancelled, "\n\nDone: Job Cancelled\n\n")

		return multistep.ActionHalt
	case <-logWriter.Timeout():
		s.writeLogAndFinishWithState(ctx, logWriter, buildJob, FinishStateErrored, fmt.Sprintf("\n\nNo output has been received in the last %v, this potentially indicates a stalled build or something wrong with the build itself.\nCheck the details on how to adjust your build configuration on: https://docs.travis-ci.com/user/common-build-problems/#Build-times-out-because-no-output-was-received\n\nThe build has been terminated\n\n", s.logTimeout))

		if s.skipShutdownOnLogTimeout {
			state.Put("skipShutdown", true)
		}

		return multistep.ActionHalt
	}
}

func (s *stepRunScript) writeLogAndFinishWithState(ctx gocontext.Context, logWriter LogWriter, buildJob Job, state FinishState, logMessage string) {
	_, err := logWriter.WriteAndClose([]byte(logMessage))
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't write final log message")
	}

	err = buildJob.Finish(state)
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).WithField("state", state).Error("couldn't update job state")
	}
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
