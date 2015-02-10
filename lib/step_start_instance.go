package lib

import (
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/mitchellh/multistep"
	"github.com/travis-ci/worker/lib/backend"
	"golang.org/x/net/context"
)

type stepStartInstance struct {
	provider     backend.Provider
	startTimeout time.Duration
}

func (s *stepStartInstance) Run(state multistep.StateBag) multistep.StepAction {
	buildJob := state.Get("buildJob").(Job)
	ctx := state.Get("ctx").(context.Context)

	ctx, cancel := context.WithTimeout(ctx, s.startTimeout)
	defer cancel()
	instance, err := s.provider.Start(ctx, buildJob.StartAttributes())
	if err != nil {
		LoggerFromContext(ctx).WithField("err", err).Error("couldn't start instance")
		buildJob.Requeue()

		return multistep.ActionHalt
	}

	state.Put("instance", instance)

	return multistep.ActionContinue
}

func (s *stepStartInstance) Cleanup(state multistep.StateBag) {
	ctx := state.Get("ctx").(context.Context)
	instance, ok := state.Get("instance").(backend.Instance)
	if !ok {
		LoggerFromContext(ctx).Info("no instance to stop")
		return
	}

	if err := instance.Stop(ctx); err != nil {
		LoggerFromContext(ctx).WithFields(logrus.Fields{"err": err, "instance": instance}).Error("couldn't stop instance")
	} else {
		LoggerFromContext(ctx).Info("stopped instance")
	}
}
