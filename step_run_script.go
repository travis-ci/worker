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
	"github.com/travis-ci/worker/metrics"
	"go.opencensus.io/trace"
)

var MaxLogLengthExceeded = errors.New("maximum log length exceeded")
var LogWriterTimeout = errors.New("log writer timeout")

// cloneAuthRemintExitCode is the build-script exit code travis-build uses to signal that the
// git clone failed with an auth 401. It tells us to regenerate the script (which re-mints a
// fresh installation token) and retry, rather than failing the job. Keep in sync with
// travis-build's Travis::Vcs::Git::Clone::CLONE_AUTH_REMINT_EXIT_CODE. GitHub ticket 4655118.
const cloneAuthRemintExitCode int32 = 89

// defaultCloneAuthRemintMax bounds the number of fresh-token clone retries, so a persistently
// rejected token (e.g. a genuinely revoked installation) fails fast instead of looping.
const defaultCloneAuthRemintMax = 2

type runScriptReturn struct {
	result *backend.RunResult
	err    error
}

type stepRunScript struct {
	logTimeout               time.Duration
	hardTimeout              time.Duration
	skipShutdownOnLogTimeout bool
	generator                BuildScriptGenerator
	cloneAuthRemintMax       int
}

func (s *stepRunScript) Run(state multistep.StateBag) multistep.StepAction {
	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	instance := state.Get("instance").(backend.Instance)
	logWriter := state.Get("logWriter").(LogWriter)
	cancelChan := state.Get("cancelChan").(<-chan CancellationCommand)

	defer context.TimeSince(ctx, "step_run_script_run", time.Now())

	ctx, span := trace.StartSpan(ctx, "RunScript.Run")
	defer span.End()

	preTimeoutCtx := ctx

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
			state.Put("err", r.err)
			logger.Info("hard timeout exceeded, terminating")
			s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum time limit for jobs, and has been terminated.\n\n")
			// Continue to the download trace step
			return multistep.ActionContinue
		}
		if logWriter.MaxLengthReached() {
			state.Put("err", MaxLogLengthExceeded)
			s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum log length, and has been terminated.\n\n")
			// Continue to the download trace step
			return multistep.ActionContinue
		}

		if r.err != nil {
			state.Put("err", r.err)

			span.SetStatus(trace.Status{
				Code:    trace.StatusCodeUnavailable,
				Message: r.err.Error(),
			})

			if !r.result.Completed {
				logger.WithFields(logrus.Fields{
					"err":       r.err,
					"completed": r.result.Completed,
				}).Error("couldn't run script, attempting requeue")
				context.CaptureError(ctx, r.err)

				err := buildJob.Requeue(preTimeoutCtx)
				if err != nil {
					logger.WithField("err", err).Error("couldn't requeue job")
				}
			} else {
				logger.WithField("err", r.err).WithField("completed", r.result.Completed).Error("couldn't run script")
				err := buildJob.Finish(preTimeoutCtx, FinishStateErrored)
				if err != nil {
					logger.WithField("err", err).Error("couldn't mark job errored")
				}
			}

			return multistep.ActionHalt
		}

		// A git-clone auth 401 comes back as a normally-completed script (err == nil) whose exit
		// code is the sentinel travis-build emits (gatekeeper-only, via the travis-build allowlist).
		// Regenerate the script -- which re-mints a fresh installation token -- and re-run, up to
		// cloneAuthRemintMax times.
		if r.result != nil && r.result.ExitCode == cloneAuthRemintExitCode {
			newResult, err := s.remintAndRerun(ctx, buildJob, instance, logWriter, logger, r.result)
			if err != nil {
				state.Put("err", err)
				logger.WithField("err", err).Error("clone-auth remint: script run failed, marking errored")
				if fErr := buildJob.Finish(preTimeoutCtx, FinishStateErrored); fErr != nil {
					logger.WithField("err", fErr).Error("couldn't mark job errored")
				}
				return multistep.ActionHalt
			}
			r.result = newResult
		}

		state.Put("scriptResult", r.result)

		return multistep.ActionContinue
	case <-ctx.Done():
		state.Put("err", ctx.Err())

		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: ctx.Err().Error(),
		})

		if ctx.Err() == gocontext.DeadlineExceeded {
			logger.Info("hard timeout exceeded, terminating")
			s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum time limit for jobs, and has been terminated.\n\n")
			// Continue to the download trace step
			return multistep.ActionContinue
		}
		if logWriter.MaxLengthReached() {
			state.Put("err", MaxLogLengthExceeded)
			s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateErrored, "\n\nThe job exceeded the maximum log length, and has been terminated.\n\n")
			// Continue to the download trace step
			return multistep.ActionContinue
		}

		logger.Info("context was cancelled, stopping job")
		return multistep.ActionHalt
	case cancelCommand := <-cancelChan:
		state.Put("err", JobCancelledError)

		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: JobCancelledError.Error(),
		})

		s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateCancelled, fmt.Sprintf("\n\nDone: Job Cancelled\n\n%s", cancelCommand.Reason))

		return multistep.ActionHalt
	case <-logWriter.Timeout():
		state.Put("err", LogWriterTimeout)

		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: LogWriterTimeout.Error(),
		})

		s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateErrored, fmt.Sprintf("\n\nNo output has been received in the last %v, this potentially indicates a stalled build or something wrong with the build itself.\nCheck the details on how to adjust your build configuration on: https://docs.travis-ci.com/user/common-build-problems/#build-times-out-because-no-output-was-received\n\nThe build has been terminated\n\n", s.logTimeout))

		if s.skipShutdownOnLogTimeout {
			state.Put("skipShutdown", true)
		}
		// Continue to the download trace step
		return multistep.ActionContinue
	}
}

// remintAndRerun handles a git-clone auth 401 (exit 89). It regenerates the build script
// (each Generate re-compiles in travis-build, minting a fresh installation token), re-uploads
// it, and re-runs it on the same instance, up to cloneAuthRemintMax attempts. It stops early as
// soon as a run returns any exit code other than the 401 sentinel (a real pass/fail, or a
// different failure). It returns the last RunResult; a non-nil error means a transport fault
// during a retry, which the caller treats like any other unrunnable script.
func (s *stepRunScript) remintAndRerun(ctx gocontext.Context, buildJob Job, instance backend.Instance, logWriter LogWriter, logger *logrus.Entry, result *backend.RunResult) (*backend.RunResult, error) {
	for attempt := 1; attempt <= s.cloneAuthRemintMax; attempt++ {
		if err := ctx.Err(); err != nil {
			return result, nil
		}

		logger.WithFields(logrus.Fields{
			"attempt": attempt,
			"max":     s.cloneAuthRemintMax,
			"job_id":  buildJob.Payload().Job.ID,
		}).Warn("git clone auth 401 (exit 89); regenerating script with a fresh token and retrying")
		metrics.Mark("worker.job.clone_auth_remint")

		script, err := s.generator.Generate(ctx, buildJob)
		if err != nil {
			logger.WithField("err", err).Error("clone-auth remint: couldn't regenerate build script, giving up")
			return result, nil
		}

		if err := instance.UploadScript(ctx, script); err != nil {
			logger.WithField("err", err).Error("clone-auth remint: couldn't upload regenerated script, giving up")
			return result, nil
		}

		newResult, err := instance.RunScript(ctx, logWriter)
		if err != nil {
			return newResult, err
		}

		result = newResult
		if result == nil || result.ExitCode != cloneAuthRemintExitCode {
			return result, nil
		}
	}

	logger.WithField("job_id", buildJob.Payload().Job.ID).Warn("git clone auth 401 persisted after clone-auth remint retries; giving up")
	return result, nil
}

func (s *stepRunScript) writeLogAndFinishWithState(preTimeoutCtx, ctx gocontext.Context, logWriter LogWriter, buildJob Job, state FinishState, logMessage string) {
	ctx, span := trace.StartSpan(ctx, "WriteLogAndFinishWithState.RunScript")
	defer span.End()

	logger := context.LoggerFromContext(ctx).WithField("self", "step_run_script")
	_, err := logWriter.WriteAndClose([]byte(logMessage))
	if err != nil {
		logger.WithField("err", err).Error("couldn't write final log message")
	}

	err = buildJob.Finish(preTimeoutCtx, state)
	if err != nil {
		logger.WithField("err", err).WithField("state", state).Error("couldn't update job state")
	}
}

func (s *stepRunScript) Cleanup(state multistep.StateBag) {}
