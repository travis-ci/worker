package backend

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"

	"github.com/travis-ci/worker/config"
	"golang.org/x/net/context"
)

var (
	ErrStaleVM               = fmt.Errorf("previous build artifacts found on stale vm")
	ErrMissingEndpointConfig = fmt.Errorf("expected config key endpoint")
	punctRegex               = regexp.MustCompile(`[&+/=\\]`)
)

// Provider represents some kind of instance provider. It can point to an
// external HTTP API, or some process locally, or something completely
// different.
type Provider interface {
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
	RunScript(context.Context, io.WriteCloser) (*RunResult, error)
	Stop(context.Context) error

	// ID is used when identifying the instance in logs and such
	ID() string
}

// StartAttributes contains some parts of the config which can be used to
// determine the type of instance to boot up (for example, what image to use)
type StartAttributes struct {
	Language string `json:"language"`
	OsxImage string `json:"osx_image"`
	Dist     string `json:"dist"`
	Group    string `json:"group"`
	OS       string `json:"os"`
}

type RunResult struct {
	ExitCode  uint8
	Completed bool
}

func NewProvider(name string, cfg *config.ProviderConfig) (Provider, error) {
	switch name {
	case "docker":
		return NewDockerProvider(cfg)
	case "sauce_labs":
		return NewSauceLabsProvider(cfg)
	case "jupiterbrain":
		return NewJupiterBrainProvider(cfg)
	case "bluebox":
		return NewBlueBoxProvider(cfg)
	case "fake":
		return NewFakeProvider([]byte("Hello to the logs")), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
}
func generatePassword() string {
	randomBytes := make([]byte, 30)
	rand.Read(randomBytes)
	hash := sha1.New().Sum(randomBytes)
	str := base64.StdEncoding.EncodeToString(hash)
	return punctRegex.ReplaceAllLiteralString(str, "")[0:19]
}
