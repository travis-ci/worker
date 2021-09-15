package backend

import (
	"context"
	"io"
	"time"

	"github.com/travis-ci/worker/config"
)

func init() {
	Register("vpc", "IBM Cloud Virtual Server for VPC", map[string]string{
		"IC_API_KEY":    "api key with access to create VMs",
		"INSTANCE_TYPE": "type of instance for each build",
	}, newFakeProvider)
}

type vpcProvider struct {
	cfg *config.ProviderConfig
}

func newVPCProvider(cfg *config.ProviderConfig) (Provider, error) {
	// TODO
	// create the API client
	return &vpcProvider{cfg: cfg}, nil
}

func (p *vpcProvider) StartWithProgress(ctx context.Context, startAttributes *StartAttributes, _ Progresser) (Instance, error) {
	// TODO
	return p.Start(ctx, startAttributes)
}

func (p *vpcProvider) Start(ctx context.Context, _ *StartAttributes) (Instance, error) {
	// TODO
	return &vpcInstance{}, nil
}

func (p *vpcProvider) Setup(ctx context.Context) error {
	// All setup is done in newVPCProvider()
	return nil
}

func (p *vpcProvider) SupportsProgress() bool {
	return false
}

type vpcInstance struct {
	p         *vpcProvider
	id        string
	imageName string
}

func (i *vpcInstance) UploadScript(ctx context.Context, script []byte) error {
	// TODO
	return nil
}

func (i *vpcInstance) RunScript(ctx context.Context, writer io.Writer) (*RunResult, error) {
	// TODO
	return &RunResult{Completed: true}, nil
}

func (i *vpcInstance) DownloadTrace(ctx context.Context) ([]byte, error) {
	// TODO
	return nil, ErrDownloadTraceNotImplemented
}

func (i *vpcInstance) Stop(ctx context.Context) error {
	// TODO
	return nil
}

func (i *vpcInstance) StartupDuration() time.Duration {
	// TODO
	return 0
}

func (i *vpcInstance) ID() string {
	return i.id
}

func (i *vpcInstance) ImageName() string {
	return i.imageName
}

func (i *vpcInstance) Warmed() bool {
	return false
}

func (i *vpcInstance) SupportsProgress() bool {
	return false
}
