package backend

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/context"
)

var (
	// ErrStaleVM is returned from one of the Instance methods if it detects
	// that the VM had already been used for a repository and was not reverted
	// afterwards.
	ErrStaleVM = fmt.Errorf("previous build artifacts found on stale vm")

	// ErrMissingEndpointConfig is returned if the provider config was missing
	// an 'ENDPOINT' configuration, but one is required.
	ErrMissingEndpointConfig = fmt.Errorf("expected config key endpoint")

	punctRegex   = regexp.MustCompile(`[&+/=\\]`)
	zeroDuration time.Duration
)

// Provider represents some kind of instance provider. It can point to an
// external HTTP API, or some process locally, or something completely
// different.
type Provider interface {
	// Setup performs whatever is necessary in order to be ready to start
	// instances.
	Setup() error

	// Start starts an instance. It shouldn't return until the instance is
	// ready to call UploadScript on (this may, for example, mean that it
	// waits for SSH connections to be possible).
	Start(context.Context, *StartAttributes) (Instance, error)
}

// An Instance is something that can run a build script.
type Instance interface {
	// UploadScript uploads the given script to the instance. The script is
	// a bash script with a shebang (#!/bin/bash) line. Note that this
	// method should not be called multiple times.
	UploadScript(context.Context, []byte) error

	// RunScript runs the build script that was uploaded with the
	// UploadScript method.
	RunScript(context.Context, io.Writer) (*RunResult, error)
	Stop(context.Context) error

	// ID is used when identifying the instance in logs and such
	ID() string

	// StartupDuration is the duration between "created" and "ready"
	StartupDuration() time.Duration
}

// RunResult represents the result of running a script with Instance.RunScript.
type RunResult struct {
	// The exit code of the script. Only valid if Completed is true.
	ExitCode uint8

	// Whether the script finished running or not. Can be false if there was a
	// connection error in the middle of the script run.
	Completed bool
}

func asBool(s string) bool {
	switch strings.ToLower(s) {
	case "0", "no", "off", "":
		return false
	default:
		return true
	}
}
