package context

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

func FromUUID(ctx context.Context, uuid string) context.Context {
	return context.WithValue(ctx, uuidKey, uuid)
}

func FromProcessor(ctx context.Context, processor string) context.Context {
	return context.WithValue(ctx, processorKey, processor)
}

func FromComponent(ctx context.Context, component string) context.Context {
	return context.WithValue(ctx, componentKey, component)
}

func FromJobID(ctx context.Context, jobID uint64) context.Context {
	return context.WithValue(ctx, jobIDKey, jobID)
}

func FromRepository(ctx context.Context, repository string) context.Context {
	return context.WithValue(ctx, repositoryKey, repository)
}

func UUIDFromContext(ctx context.Context) (string, bool) {
	uuid, ok := ctx.Value(uuidKey).(string)
	return uuid, ok
}

func ProcessorFromContext(ctx context.Context) (string, bool) {
	processor, ok := ctx.Value(processorKey).(string)
	return processor, ok
}

func ComponentFromContext(ctx context.Context) (string, bool) {
	component, ok := ctx.Value(componentKey).(string)
	return component, ok
}

func JobIDFromContext(ctx context.Context) (uint64, bool) {
	jobID, ok := ctx.Value(jobIDKey).(uint64)
	return jobID, ok
}

func RepositoryFromContext(ctx context.Context) (string, bool) {
	repository, ok := ctx.Value(repositoryKey).(string)
	return repository, ok
}

func LoggerFromContext(ctx context.Context) *logrus.Entry {
	entry := logrus.WithField("pid", os.Getpid())

	if uuid, ok := UUIDFromContext(ctx); ok {
		entry = entry.WithField("uuid", uuid)
	}

	if processor, ok := ProcessorFromContext(ctx); ok {
		entry = entry.WithField("processor", processor)
	}

	if component, ok := ComponentFromContext(ctx); ok {
		entry = entry.WithField("component", component)
	}

	if jobID, ok := JobIDFromContext(ctx); ok {
		entry = entry.WithField("job", jobID)
	}

	if repository, ok := RepositoryFromContext(ctx); ok {
		entry = entry.WithField("repository", repository)
	}

	return entry
}
