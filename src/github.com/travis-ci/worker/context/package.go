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

// FromUUID generates a new context with the given context as its parent and
// stores the given UUID with the context. The UUID can be retrieved again using
// UUIDFromContext.
func FromUUID(ctx context.Context, uuid string) context.Context {
	return context.WithValue(ctx, uuidKey, uuid)
}

// FromProcessor generates a new context with the given context as its parent
// and stores the given processor ID with the context. The processor ID can be
// retrieved again using ProcessorFromContext.
func FromProcessor(ctx context.Context, processor string) context.Context {
	return context.WithValue(ctx, processorKey, processor)
}

// FromComponent generates a new context with the given context as its parent
// and stores the given component name with the context. The component name can
// be retrieved again using ComponentFromContext.
func FromComponent(ctx context.Context, component string) context.Context {
	return context.WithValue(ctx, componentKey, component)
}

// FromJobID generates a new context with the given context as its parent and
// stores the given job ID with the context. The job ID can be retrieved again
// using JobIDFromContext.
func FromJobID(ctx context.Context, jobID uint64) context.Context {
	return context.WithValue(ctx, jobIDKey, jobID)
}

// FromRepository generates a new context with the given context as its parent
// and stores the given repository name with the context. The repository name
// can be retrieved again using RepositoryFromContext.
func FromRepository(ctx context.Context, repository string) context.Context {
	return context.WithValue(ctx, repositoryKey, repository)
}

// UUIDFromContext returns the UUID stored in the context with FromUUID. If no
// UUID was stored in the context, the second argument is false. Otherwise it is
// true.
func UUIDFromContext(ctx context.Context) (string, bool) {
	uuid, ok := ctx.Value(uuidKey).(string)
	return uuid, ok
}

// ProcessorFromContext returns the processor name stored in the context with
// FromProcessor. If no processor name was stored in the context, the second
// argument is false. Otherwise it is true.
func ProcessorFromContext(ctx context.Context) (string, bool) {
	processor, ok := ctx.Value(processorKey).(string)
	return processor, ok
}

// ComponentFromContext returns the component name stored in the context with
// FromComponent. If no component name was stored in the context, the second
// argument is false. Otherwise it is true.
func ComponentFromContext(ctx context.Context) (string, bool) {
	component, ok := ctx.Value(componentKey).(string)
	return component, ok
}

// JobIDFromContext returns the job ID stored in the context with FromJobID. If
// no job ID was stored in the context, the second argument is false. Otherwise
// it is true.
func JobIDFromContext(ctx context.Context) (uint64, bool) {
	jobID, ok := ctx.Value(jobIDKey).(uint64)
	return jobID, ok
}

// RepositoryFromContext returns the repository name stored in the context with
// FromRepository. If no repository name was stored in the context, the second
// argument is false. Otherwise it is true.
func RepositoryFromContext(ctx context.Context) (string, bool) {
	repository, ok := ctx.Value(repositoryKey).(string)
	return repository, ok
}

// LoggerFromContext returns a logrus.Entry with the PID of the current process
// set as a field, and also includes every field set using the From* functions
// this package.
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
