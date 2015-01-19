package backend

import (
	"io"

	"golang.org/x/net/context"
)

type FakeProvider struct {
	logOutput []byte
}

func NewFakeProvider(logOutput []byte) *FakeProvider {
	return &FakeProvider{logOutput: logOutput}
}

func (p *FakeProvider) Start(ctx context.Context) (Instance, error) {
	return &FakeInstance{logOutput: p.logOutput}, nil
}

type FakeInstance struct {
	logOutput []byte
}

func (i *FakeInstance) UploadScript(ctx context.Context, script []byte) error {
	return nil
}

func (i *FakeInstance) RunScript(ctx context.Context, writer io.Writer) (RunResult, error) {
	_, err := writer.Write(i.logOutput)
	if err != nil {
		return RunResult{Completed: false}, err
	}

	return RunResult{Completed: true}, nil
}

func (i *FakeInstance) Stop(ctx context.Context) error {
	return nil
}
