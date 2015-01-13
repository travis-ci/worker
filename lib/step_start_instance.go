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

	instance, err := s.provider.Start(ctx)
	if err != nil {
		loggerFromContext(ctx).WithField("err", err).Error("couldn't start instance")
		buildJob.Requeue()

		return multistep.ActionHalt
	}

	state.Put("instance", instance)

	return multistep.ActionContinue
}

func (s *stepStartInstance) Cleanup(state multistep.StateBag) {
	instance := state.Get("instance").(backend.Instance)
	ctx := state.Get("ctx").(context.Context)

	if err := instance.Stop(ctx); err != nil {
		loggerFromContext(ctx).WithFields(logrus.Fields{"err": err, "instance": instance}).Error("couldn't stop instance")
	} else {
		loggerFromContext(ctx).Info("stopped instance")
	}
}
