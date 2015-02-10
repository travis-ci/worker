package lib

import (
	"os"

	"github.com/Sirupsen/logrus"
	"golang.org/x/net/context"
)

type contextKey int

const (
	uuidKey contextKey = iota
	processorKey
	componentKey
	jobIDKey
	repositoryKey
)

func contextFromUUID(ctx context.Context, uuid string) context.Context {
	return context.WithValue(ctx, uuidKey, uuid)
}

func contextFromProcessor(ctx context.Context, processor string) context.Context {
	return context.WithValue(ctx, processorKey, processor)
}

func contextFromComponent(ctx context.Context, component string) context.Context {
	return context.WithValue(ctx, componentKey, component)
}

func contextFromJobID(ctx context.Context, jobID uint64) context.Context {
	return context.WithValue(ctx, jobIDKey, jobID)
}

func contextFromRepository(ctx context.Context, repository string) context.Context {
	return context.WithValue(ctx, repositoryKey, repository)
}

func contextFromJob(ctx context.Context, job Job) context.Context {
	return contextFromUUID(contextFromJobID(contextFromRepository(ctx, job.Payload().Repository.Slug), job.Payload().Job.ID), job.Payload().UUID)
}

func uuidFromContext(ctx context.Context) (string, bool) {
	uuid, ok := ctx.Value(uuidKey).(string)
	return uuid, ok
}

func processorFromContext(ctx context.Context) (string, bool) {
	processor, ok := ctx.Value(processorKey).(string)
	return processor, ok
}

func componentFromContext(ctx context.Context) (string, bool) {
	component, ok := ctx.Value(componentKey).(string)
	return component, ok
}

func jobIDFromContext(ctx context.Context) (uint64, bool) {
	jobID, ok := ctx.Value(jobIDKey).(uint64)
	return jobID, ok
}

func repositoryFromContext(ctx context.Context) (string, bool) {
	repository, ok := ctx.Value(repositoryKey).(string)
	return repository, ok
}

func LoggerFromContext(ctx context.Context) *logrus.Entry {
	entry := logrus.WithField("pid", os.Getpid())

	if uuid, ok := uuidFromContext(ctx); ok {
		entry = entry.WithField("uuid", uuid)
	}

	if processor, ok := processorFromContext(ctx); ok {
		entry = entry.WithField("processor", processor)
	}

	if component, ok := componentFromContext(ctx); ok {
		entry = entry.WithField("component", component)
	}

	if jobID, ok := jobIDFromContext(ctx); ok {
		entry = entry.WithField("job", jobID)
	}

	if repository, ok := repositoryFromContext(ctx); ok {
		entry = entry.WithField("repository", repository)
	}

	return entry
}
