package backend

import (
	"io"
	"time"

	"github.com/codegangsta/cli"

	"golang.org/x/net/context"
)

func init() {
	Register("fake", "Fake", []cli.Flag{
		backendStringFlag("fake", "log-output", "", "LOG_OUTPUT", "Faked log output to write"),
	}, newFakeProvider)
}

type fakeProvider struct {
	startupDuration time.Duration
	logOutput       string
}

func newFakeProvider(c ConfigGetter) (Provider, error) {
	return &fakeProvider{
		startupDuration: c.Duration("startup-duration"),
		logOutput:       c.String("log-output"),
	}, nil
}

func (p *fakeProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	return &fakeInstance{
		startupDuration: p.startupDuration,
		logOutput:       p.logOutput,
	}, nil
}

func (p *fakeProvider) Setup() error { return nil }

type fakeInstance struct {
	startupDuration time.Duration
	logOutput       string
}

func (i *fakeInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *fakeInstance) RunScript(ctx context.Context, writer io.Writer) (*RunResult, error) {
	_, err := writer.Write([]byte(i.logOutput))
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
