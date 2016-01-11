package backend

import (
	"io"
	"time"

	"github.com/codegangsta/cli"

	"golang.org/x/net/context"
)

func init() {
	Register("fake", "Fake", []cli.Flag{
		backendStringFlag("fake", "log-output", "",
			"Faked log output to write",
			[]string{"LOG_OUTPUT"}),
	}, newFakeProvider)
}

type fakeProvider struct {
	c *cli.Context
}

func newFakeProvider(c *cli.Context) (Provider, error) {
	return &fakeProvider{c: c}, nil
}

func (p *fakeProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	var (
		dur time.Duration
		err error
	)

	if p.c.String("startup-duration") != "" {
		dur, err = time.ParseDuration(p.c.String("startup-duration"))
		if err != nil {
			return nil, err
		}
	}

	return &fakeInstance{p: p, startupDuration: dur}, nil
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
	_, err := writer.Write([]byte(i.p.c.String("log-output")))
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
