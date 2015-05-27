package lib

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"github.com/travis-ci/worker/lib/context"
	gocontext "golang.org/x/net/context"
)

type stepStartInstance struct {
	provider     backend.Provider
	startTimeout time.Duration
}

func (s *stepStartInstance) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(gocontext.Context)

	context.LoggerFromContext(ctx).Info("starting instance")

	ctx, cancel := gocontext.WithTimeout(ctx, s.startTimeout)
	defer cancel()

	startTime := time.Now()

	instance, err := s.provider.Start(ctx, buildJob.StartAttributes())
	if err != nil {
		context.LoggerFromContext(ctx).WithField("err", err).Error("couldn't start instance")
		buildJob.Requeue()

		return multistep.ActionHalt
	}

	context.LoggerFromContext(ctx).WithField("boot_time", time.Now().Sub(startTime)).Info("started instance")

	state.Put("instance", instance)

	return multistep.ActionContinue
}

func (s *stepStartInstance) Cleanup(state multistep.StateBag) {
	ctx := state.Get("ctx").(gocontext.Context)
	instance, ok := state.Get("instance").(backend.Instance)
	if !ok {
		context.LoggerFromContext(ctx).Info("no instance to stop")
		return
	}

	skipShutdown, ok := state.Get("skipShutdown").(bool)
	if ok && skipShutdown {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{"err": err, "instance": instance}).Error("skipping shutdown, VM will be left running")
		return
	}

	if err := instance.Stop(ctx); err != nil {
		context.LoggerFromContext(ctx).WithFields(logrus.Fields{"err": err, "instance": instance}).Error("couldn't stop instance")
	} else {
		context.LoggerFromContext(ctx).Info("stopped instance")
	}
}
