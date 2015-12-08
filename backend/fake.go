package backend

import (
	"io"
	"time"

	"github.com/travis-ci/worker/config"

	"golang.org/x/net/context"
)

func init() {
	Register("fake", "Fake", map[string]string{
		"LOG_OUTPUT": "faked log output to write",
	}, newFakeProvider)
}

type fakeProvider struct {
	cfg *config.ProviderConfig
}

func newFakeProvider(cfg *config.ProviderConfig) (Provider, error) {
	return &fakeProvider{cfg: cfg}, nil
}

func (p *fakeProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	return &fakeInstance{p: p, startupDuration: 42170 * time.Millisecond}, nil
}

func (p *fakeProvider) Setup() error { return nil }

type fakeInstance struct {
	p *fakeProvider

	startupDuration time.Duration
}

func (i *fakeInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *fakeInstance) RunScript(ctx context.Context, writer io.Writer) (*RunResult, error) {
	_, err := writer.Write([]byte(i.p.cfg.Get("LOG_OUTPUT")))
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	return &RunResult{Completed: true}, nil
}

func (i *fakeInstance) Stop(ctx context.Context) error {
	return nil
}

func (i *fakeInstance) ID() string {
	return "fake"
}

func (i *fakeInstance) StartupDuration() time.Duration {
	return i.startupDuration
}
