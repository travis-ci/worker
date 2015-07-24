package backend

import (
	"io"

	"github.com/travis-ci/worker/config"

	"golang.org/x/net/context"
)

type fakeProvider struct {
	cfg *config.ProviderConfig
}

func newFakeProvider(cfg *config.ProviderConfig) *fakeProvider {
	return &fakeProvider{cfg: cfg}
}

func (p *fakeProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	return &fakeInstance{p: p}, nil
}

type fakeInstance struct {
	p *fakeProvider
}

func (i *fakeInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *fakeInstance) RunScript(ctx context.Context, writer io.WriteCloser) (*RunResult, error) {
	_, err := writer.Write([]byte(i.p.cfg.Get("LOG_OUTPUT")))
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	err = writer.Close()
	if err != nil {
		return &RunResult{Completed: true}, err
	}

	return &RunResult{Completed: true}, nil
}

func (i *fakeInstance) Stop(ctx context.Context) error {
	return nil
}

func (i *fakeInstance) ID() string {
	return "fake"
}
