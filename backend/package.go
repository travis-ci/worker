/*
Package backend provides the compute instance backends supported by Worker.

Other code will primarily interact with this package by creating a Provider
implementation and creating Instances. An example using the "fake" provider
(error handling omitted):

	provider := backend.NewBackendProvider(
		"fake",
		config.ProviderConfigFromMap(map[string]string{
			"STARTUP_DURATION": "1s",
			"LOG_OUTPUT": "Hello, world!",
		}),
	)

	provider.Setup(ctx)

	instance, _ := provider.Start(ctx, &backend.StartAttributes{
		Language: "go",
		OS: "linux",
	})
	defer instance.Stop(ctx)

	instance.UploadScript(ctx, []byte("#!/bin/sh\necho 'Hello, world!'))
	instance.RunScript(ctx, os.Stdout)

New providers should call Register in init() to register the alias it should be called with and the options it supports for the --help output.
*/
package backend

import (
	gocontext "context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pborman/uuid"
	"github.com/travis-ci/worker/context"
)

var (
	// ErrStaleVM is returned from one of the Instance methods if it detects
	// that the VM had already been used for a repository and was not reverted
	// afterwards.
	ErrStaleVM = fmt.Errorf("previous build artifacts found on stale vm")

	// ErrMissingEndpointConfig is returned if the provider config was missing
	// an 'ENDPOINT' configuration, but one is required.
	ErrMissingEndpointConfig = fmt.Errorf("expected config key endpoint")

	zeroDuration time.Duration

	hostnamePartsDisallowed = regexp.MustCompile("[^a-zA-Z0-9-]+")
	multiDash               = regexp.MustCompile("-+")
)

// Provider represents some kind of instance provider. It can point to an
// external HTTP API, or some process locally, or something completely
// different.
type Provider interface {
	// Setup performs whatever is necessary in order to be ready to start
	// instances.
	Setup(gocontext.Context) error

	// Start starts an instance. It shouldn't return until the instance is
	// ready to call UploadScript on (this may, for example, mean that it
	// waits for SSH connections to be possible).
	Start(gocontext.Context, *StartAttributes) (Instance, error)

	// StartWithProgress starts an instance as with Start and also reports
	// progress via an io.Writer.
	StartWithProgress(gocontext.Context, *StartAttributes, Progresser) (Instance, error)

	// SupportsProgress allows for querying of progress support, yeah!
	SupportsProgress() bool
}

// An Instance is something that can run a build script.
type Instance interface {
	// UploadScript uploads the given script to the instance. The script is
	// a bash script with a shebang (#!/bin/bash) line. Note that this
	// method should not be called multiple times.
	UploadScript(gocontext.Context, []byte) error

	// RunScript runs the build script that was uploaded with the
	// UploadScript method.
	RunScript(gocontext.Context, io.Writer) (*RunResult, error)

	// DownloadTrace attempts to download a job trace from the instance
	DownloadTrace(gocontext.Context) ([]byte, error)

	// StopOnly stops the instance without deletion
	StopOnly(gocontext.Context) error

	// Stop stops (and deletes) the instance
	Stop(gocontext.Context) error

	// CreateImage creates image from the instance, returns size, arch, os of image and error
	CreateImage(gocontext.Context, string, func(string)) (int64, string, string, error)

	// ID is used when identifying the instance in logs and such
	ID() string

	// ImageName is the name of the image used to boot the instance
	ImageName() string

	// StartupDuration is the duration between "created" and "ready"
	StartupDuration() time.Duration

	// SupportsProgress allows for querying of progress support, yeah!
	SupportsProgress() bool

	// Check if this instance came from the warmer service
	Warmed() bool
}

// RunResult represents the result of running a script with Instance.RunScript.
type RunResult struct {
	// The exit code of the script. Only valid if Completed is true.
	ExitCode int32

	// Whether the script finished running or not. Can be false if there was a
	// connection error in the middle of the script run.
	Completed bool
}

func asBool(s string) bool {
	switch strings.ToLower(s) {
	case "0", "no", "off", "false", "":
		return false
	default:
		return true
	}
}

func str2map(s, separators string) map[string]string {
	ret := map[string]string{}

	f := func(r rune) bool {
		return strings.ContainsAny(string(r), separators)
	}

	for _, kv := range strings.FieldsFunc(s, f) {
		kvParts := strings.SplitN(kv, ":", 2)
		key := strings.TrimSpace(kvParts[0])
		if key == "" {
			continue
		}
		if len(kvParts) == 1 {
			ret[key] = ""
		} else {
			val := strings.TrimSpace(kvParts[1])
			val, _ = url.QueryUnescape(val)
			ret[key] = val
		}
	}

	return ret
}

func hostnameFromContext(ctx gocontext.Context) string {
	randName := fmt.Sprintf("travis-job-unk-unk-%s", uuid.NewRandom())
	jobID, ok := context.JobIDFromContext(ctx)
	if !ok {
		return randName
	}

	repoName, ok := context.RepositoryFromContext(ctx)
	if !ok {
		return randName
	}

	nameParts := []string{"travis-job"}
	for _, part := range strings.Split(repoName, "/") {
		cleanedPart := hostnamePartsDisallowed.ReplaceAllString(part, "-")
		// NOTE: the part limit of 14 is meant to ensure a maximum hostname of
		// 64 characters, given:
		// travis-job-{part}-{part}-{job-id}.travisci.net
		// ^---11----^^--15-^^--15-^^--11---^^---12-----^
		// therefore:
		// 11 + 15 + 15 + 11 + 12 = 64
		if len(cleanedPart) > 14 {
			cleanedPart = cleanedPart[0:14]
		}
		nameParts = append(nameParts, cleanedPart)
	}

	joined := strings.Join(append(nameParts, fmt.Sprintf("%v", jobID)), "-")
	return strings.ToLower(multiDash.ReplaceAllString(joined, "-"))
}
