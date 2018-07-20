package worker

import (
	"bytes"
	"time"

	gocontext "context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/mitchellh/multistep"
	"github.com/sirupsen/logrus"
	"github.com/travis-ci/worker/backend"
	"github.com/travis-ci/worker/context"
	"github.com/travis-ci/worker/metrics"
)

type stepDownloadTrace struct {
	enabled            bool
	archiveS3Bucket    string
	archiveS3KeyPrefix string
	archiveS3Region    string
}

func (s *stepDownloadTrace) Run(state multistep.StateBag) multistep.StepAction {
	if !s.enabled {
		return multistep.ActionContinue
	}

	ctx := state.Get("ctx").(gocontext.Context)
	buildJob := state.Get("buildJob").(Job)
	processedAt := state.Get("processedAt").(time.Time)

	instance := state.Get("instance").(backend.Instance)

	logger := context.LoggerFromContext(ctx).WithField("self", "step_download_trace")

	// ctx, cancel := gocontext.WithTimeout(ctx, s.uploadTimeout)
	// defer cancel()

	// downloading the trace is best-effort, so we continue in any case

	buf, err := instance.DownloadTrace(ctx)
	if err != nil {
		metrics.Mark("worker.job.trace.download.error")

		logger.WithFields(logrus.Fields{
			"err": err,
		}).Error("couldn't download trace")
		context.CaptureError(ctx, err)

		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"since_processed_ms": time.Since(processedAt).Seconds() * 1e3,
	}).Info("downloaded trace")

	// TODO: aws credentials
	// TODO: goroutine pool for aws session reuse?

	sess, err := session.NewSession(&aws.Config{Region: aws.String(s.archiveS3Region)})
	if err != nil {
		metrics.Mark("worker.job.trace.archive.error")

		logger.WithFields(logrus.Fields{
			"err": err,
		}).Error("couldn't archive trace")
		context.CaptureError(ctx, err)

		return multistep.ActionContinue
	}

	// TODO: handle job restarts -- archive separate trace per run? use job UUID?

	key := s.archiveS3KeyPrefix + "/" + string(buildJob.Payload().Job.ID)

	_, err = s3.New(sess).PutObject(&s3.PutObjectInput{
		Bucket:               aws.String(s.archiveS3Bucket),
		Key:                  aws.String(key),
		ACL:                  aws.String("private"),
		Body:                 bytes.NewReader(buf),
		ContentLength:        aws.Int64(int64(len(buf))),
		ContentType:          aws.String("application/octet-stream"),
		ContentDisposition:   aws.String("attachment"),
		ServerSideEncryption: aws.String("AES256"),
	})
	if err != nil {
		metrics.Mark("worker.job.trace.archive.error")

		logger.WithFields(logrus.Fields{
			"err": err,
		}).Error("couldn't archive trace")
		context.CaptureError(ctx, err)

		return multistep.ActionContinue
	}

	logger.WithFields(logrus.Fields{
		"since_processed_ms": time.Since(processedAt).Seconds() * 1e3,
	}).Info("archived trace")

	return multistep.ActionContinue
}

func (s *stepDownloadTrace) Cleanup(state multistep.StateBag) {
	// Nothing to clean up
}
