package backend

import (
	"context"
	gocontext "context"
	"errors"
	"io"
	"time"

	"github.com/travis-ci/worker/config"
)

func init() {
	Register("fake", "Fake", map[string]string{
		"LOG_OUTPUT": "faked log output to write",
		"RUN_SLEEP":  "faked runtime sleep duration",
		"ERROR":      "error out all jobs (useful for testing requeue storms)",
	}, newFakeProvider)
}

type fakeProvider struct {
	cfg *config.ProviderConfig
}

func newFakeProvider(cfg *config.ProviderConfig) (Provider, error) {
	return &fakeProvider{cfg: cfg}, nil
}

func (p *fakeProvider) SupportsProgress() bool {
	return false
}

func (p *fakeProvider) StartWithProgress(ctx context.Context, startAttributes *StartAttributes, _ Progresser) (Instance, error) {
	return p.Start(ctx, startAttributes)
}

func (p *fakeProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	var (
		dur time.Duration
		err error
	)

	if p.cfg.IsSet("STARTUP_DURATION") {
		dur, err = time.ParseDuration(p.cfg.Get("STARTUP_DURATION"))
		if err != nil {
			return nil, err
		}
	}

	return &fakeInstance{p: p, startupDuration: dur}, nil
}

func (p *fakeProvider) Setup(ctx context.Context) error { return nil }

type fakeInstance struct {
	p *fakeProvider

	startupDuration time.Duration
}

func (i *fakeInstance) Warmed() bool {
	return false
}

func (i *fakeInstance) SupportsProgress() bool {
	return false
}

func (i *fakeInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *fakeInstance) RunScript(ctx context.Context, writer io.Writer) (*RunResult, error) {
	if i.p.cfg.Get("ERROR") == "true" {
		return &RunResult{Completed: false}, errors.New("fake provider is configured to error all jobs")
	}

	if i.p.cfg.IsSet("RUN_SLEEP") {
		rs, err := time.ParseDuration(i.p.cfg.Get("RUN_SLEEP"))
		if err != nil {
			return &RunResult{Completed: false}, err
		}
		time.Sleep(rs)
	}

	_, err := writer.Write([]byte(i.p.cfg.Get("LOG_OUTPUT")))
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	return &RunResult{Completed: true}, nil
}

func (i *fakeInstance) DownloadTrace(ctx context.Context) ([]byte, error) {
	return nil, ErrDownloadTraceNotImplemented
}

func (i *fakeInstance) StopOnly(ctx context.Context) error {
	return nil
}

func (i *fakeInstance) CreateImage(ctx gocontext.Context, createCustomImageName string, logWriterFunc func(string)) (int64, string, string, error) {
	return 0, "", "", nil
}

func (i *fakeInstance) Stop(ctx context.Context) error {
	return nil
}

func (i *fakeInstance) ID() string {
	return "fake"
}

func (i *fakeInstance) ImageName() string {
	return "fake"
}

func (i *fakeInstance) StartupDuration() time.Duration {
	return i.startupDuration
}
