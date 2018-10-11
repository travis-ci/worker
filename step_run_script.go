package worker

import (
	"fmt"
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"go.opencensus.io/trace"
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
	procCtx := state.Get("procCtx").(gocontext.Context)
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	instance := state.Get("instance").(backend.Instance)
	logWriter := state.Get("logWriter").(LogWriter)
	cancelChan := state.Get("cancelChan").(<-chan struct{})

	ctx, span := trace.StartSpan(ctx, "stepRunScript")
	defer span.End()

	logger := context.LoggerFromContext(ctx).WithField("self", "step_run_script")
	ctx, cancel := gocontext.WithTimeout(ctx, s.hardTimeout)
	logWriter.SetCancelFunc(cancel)
	defer cancel()

	logger.Info("running script")
	defer logger.Info("finished script")

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
		// We need to check for this since it's possible that the RunScript
		// implementation returns with the error too quickly for the ctx.Done()
		// case branch below to catch it.
		if errors.Cause(r.err) == gocontext.DeadlineExceeded {
			logger.Info("hard timeout exceeded, terminating")
			s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum time limit for jobs, and has been terminated.\n\n")
			return multistep.ActionHalt
		}
		if logWriter.MaxLengthReached() {
			s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum log length, and has been terminated.\n\n")
			return multistep.ActionHalt
		}

		if r.err != nil {
			if !r.result.Completed {
				logger.WithFields(logrus.Fields{
					"err":       r.err,
					"completed": r.result.Completed,
				}).Error("couldn't run script, attempting requeue")
				context.CaptureError(ctx, r.err)

				err := buildJob.Requeue(procCtx)
				if err != nil {
					logger.WithField("err", err).Error("couldn't requeue job")
				}
			} else {
				logger.WithField("err", r.err).WithField("completed", r.result.Completed).Error("couldn't run script")
				err := buildJob.Finish(procCtx, FinishStateErrored)
				if err != nil {
					logger.WithField("err", err).Error("couldn't mark job errored")
				}
			}

			return multistep.ActionHalt
		}

		state.Put("scriptResult", r.result)

		return multistep.ActionContinue
	case <-ctx.Done():
		if ctx.Err() == gocontext.DeadlineExceeded {
			logger.Info("hard timeout exceeded, terminating")
			s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum time limit for jobs, and has been terminated.\n\n")
			return multistep.ActionHalt
		}
		if logWriter.MaxLengthReached() {
			s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum log length, and has been terminated.\n\n")
			return multistep.ActionHalt
		}

		logger.Info("context was cancelled, stopping job")
		return multistep.ActionHalt
	case <-cancelChan:
		s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateCancelled, "\n\nDone: Job Cancelled\n\n")

		return multistep.ActionHalt
	case <-logWriter.Timeout():
		s.writeLogAndFinishWithState(procCtx, ctx, logWriter, buildJob, FinishStateErrored, fmt.Sprintf("\n\nNo output has been received in the last %v, this potentially indicates a stalled build or something wrong with the build itself.\nCheck the details on how to adjust your build configuration on: https://docs.travis-ci.com/user/common-build-problems/#Build-times-out-because-no-output-was-received\n\nThe build has been terminated\n\n", s.logTimeout))

		if s.skipShutdownOnLogTimeout {
			state.Put("skipShutdown", true)
		}

		return multistep.ActionHalt
	}
}

func (s *stepRunScript) writeLogAndFinishWithState(procCtx, ctx gocontext.Context, logWriter LogWriter, buildJob Job, state FinishState, logMessage string) {
	ctx, span := trace.StartSpan(ctx, "stepWriteLogAndFinishWithState")
	defer span.End()

	logger := context.LoggerFromContext(ctx).WithField("self", "step_run_script")
	_, err := logWriter.WriteAndClose([]byte(logMessage))
	if err != nil {
		logger.WithField("err", err).Error("couldn't write final log message")
	}

	err = buildJob.Finish(procCtx, state)
	if err != nil {
		logger.WithField("err", err).WithField("state", state).Error("couldn't update job state")
	}
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {}
