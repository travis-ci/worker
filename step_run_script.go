package worker

import (
	"fmt"
	"io"
	"time"

	gocontext "context"

	"github.com/mitchellh/multistep"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	agent "github.com/travis-ci/worker-agent/agent"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"go.opencensus.io/trace"
	"google.golang.org/grpc"
)

var MaxLogLengthExceeded = errors.New("maximum log length exceeded")
var LogWriterTimeout = errors.New("log writer timeout")

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
		var result *backend.RunResult
		var err error
		// TODO: config option
		agentEnabled := true
		if agentEnabled {
			result, err = s.runScriptWithAgent(ctx, logWriter, instance)
		} else {
			result, err = instance.RunScript(ctx, logWriter)
		}
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
	case <-cancelChan:
		state.Put("err", JobCancelledError)

		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: JobCancelledError.Error(),
		})

		s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateCancelled, "\n\nDone: Job Cancelled\n\n")

		return multistep.ActionHalt
	case <-logWriter.Timeout():
		state.Put("err", LogWriterTimeout)

		span.SetStatus(trace.Status{
			Code:    trace.StatusCodeUnavailable,
			Message: LogWriterTimeout.Error(),
		})

		s.writeLogAndFinishWithState(preTimeoutCtx, ctx, logWriter, buildJob, FinishStateErrored, fmt.Sprintf("\n\nNo output has been received in the last %v, this potentially indicates a stalled build or something wrong with the build itself.\nCheck the details on how to adjust your build configuration on: https://docs.travis-ci.com/user/common-build-problems/#Build-times-out-because-no-output-was-received\n\nThe build has been terminated\n\n", s.logTimeout))

		if s.skipShutdownOnLogTimeout {
			state.Put("skipShutdown", true)
		}
		// Continue to the download trace step
		return multistep.ActionContinue
	}
}

func (s *stepRunScript) runScriptWithAgent(ctx gocontext.Context, logWriter LogWriter, instance backend.Instance) (*backend.RunResult, error) {
	ip, err := instance.IP(ctx)
	if err != nil {
		return &backend.RunResult{Completed: false}, errors.Wrap(err, "could not get instance ip")
	}
	address := ip + ":" + agent.PORT

	// TODO: figure out grpc security
	conn, err := grpc.Dial(address, grpc.WithInsecure())
	if err != nil {
		return &backend.RunResult{Completed: false}, errors.Wrap(err, "could not connect to agent")
	}
	defer conn.Close()

	c := agent.NewAgentClient(conn)

	// TODO: get these values from state
	// TODO: get command and args from instance
	_, err = c.RunJob(ctx, &agent.RunJobRequest{
		JobId:        "123",
		Command:      "bash",
		CommandArgs:  []string{"build.sh"},
		LogTimeoutS:  10,
		HardTimeoutS: 10,
		MaxLogLength: 10,
	})

	logger := context.LoggerFromContext(ctx).WithField("self", "step_run_script")

	if err != nil {
		return &backend.RunResult{Completed: false}, errors.Wrap(err, "could not run job")
	}

	stream, err := c.GetLogParts(ctx, &agent.LogPartsRequest{})
	if err != nil {
		logger.WithField("err", err).Error("error trying to GetLogParts")
		return &backend.RunResult{Completed: false}, errors.Wrap(err, "could not get log parts")
	}

	// TODO: figure out how to persist offset
	// offset := int64(0)

	for {
		part, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.WithField("err", err).Error("couldn't get log parts from stream")
			return &backend.RunResult{Completed: false}, errors.Wrap(err, "could not get log parts")
		}
		logWriter.Write([]byte(part.Content))

		// offset = part.Number
	}

	st, err := c.GetJobStatus(ctx, &agent.WorkerRequest{})
	if err != nil {
		logger.WithField("err", err).Error("couldn't get job status")
		return &backend.RunResult{Completed: false}, errors.Wrap(err, "could not get job status")
	}

	return &backend.RunResult{
		Completed: true,
		ExitCode:  st.ExitCode,
	}, nil
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
