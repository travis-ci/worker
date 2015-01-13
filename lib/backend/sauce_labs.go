package backend

import "golang.org/x/net/context"

type sauceLabsProvider struct{}
type sauceLabsInstance struct{}

func NewSauceLabsProvider(config string) Provider {
	return &sauceLabsProvider{}
}

func (p *sauceLabsProvider) Start(ctx context.Context) (Instance, error) {
	return nil, nil
}

func (i *sauceLabsInstance) RunScript(ctx context.Context, script []byte) (RunResult, error) {
	return RunResult{}, nil
}

func (i *sauceLabsInstance) Stop(ctx context.Context) error {
	return nil
}
