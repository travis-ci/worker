package backend

import (
	"io"

	"golang.org/x/net/context"
)

type fakeProvider struct {
	logOutput []byte
}

func newFakeProvider(logOutput []byte) *fakeProvider {
	return &fakeProvider{logOutput: logOutput}
}

func (p *fakeProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	return &fakeInstance{logOutput: p.logOutput}, nil
}

type fakeInstance struct {
	logOutput []byte
}

func (i *fakeInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *fakeInstance) RunScript(ctx context.Context, writer io.Writer) (*RunResult, error) {
	_, err := writer.Write(i.logOutput)
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
