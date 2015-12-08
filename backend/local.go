package backend

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/travis-ci/worker/config"
	gocontext "golang.org/x/net/context"
)

var (
	errNoScriptUploaded = fmt.Errorf("no script uploaded")
	localHelp           = map[string]string{
		"SCRIPTS_DIR": "directory where generated scripts will be written",
	}
)

func init() {
	Register("local", "Local", localHelp, newLocalProvider)
}

type localProvider struct {
	cfg        *config.ProviderConfig
	scriptsDir string
}

func newLocalProvider(cfg *config.ProviderConfig) (Provider, error) {
	scriptsDir, _ := os.Getwd()

	if cfg.IsSet("SCRIPTS_DIR") {
		scriptsDir = cfg.Get("SCRIPTS_DIR")
	}

	if scriptsDir == "" {
		scriptsDir = os.TempDir()
	}

	return &localProvider{cfg: cfg, scriptsDir: scriptsDir}, nil
}

func (p *localProvider) Start(ctx gocontext.Context, startAttributes *StartAttributes) (Instance, error) {
	return newLocalInstance(p)
}

func (p *localProvider) Setup() error { return nil }

type localInstance struct {
	p *localProvider

	scriptPath string
}

func newLocalInstance(p *localProvider) (*localInstance, error) {
	return &localInstance{
		p: p,
	}, nil
}

func (i *localInstance) UploadScript(ctx gocontext.Context, script []byte) error {
	scriptPath := filepath.Join(i.p.scriptsDir, fmt.Sprintf("build-%v.sh", time.Now().UTC().UnixNano()))
	f, err := os.Create(scriptPath)
	if err != nil {
		return err
	}

	i.scriptPath = scriptPath

	scriptBuf := bytes.NewBuffer(script)
	_, err = io.Copy(f, scriptBuf)
	return err
}

func (i *localInstance) RunScript(ctx gocontext.Context, writer io.Writer) (*RunResult, error) {
	if i.scriptPath == "" {
		return &RunResult{Completed: false}, errNoScriptUploaded
	}

	cmd := exec.Command("bash", i.scriptPath)
	cmd.Stdout = writer
	cmd.Stderr = writer

	err := cmd.Start()
	if err != nil {
		return &RunResult{Completed: false}, err
	}

	errChan := make(chan error)
	go func() {
		errChan <- cmd.Wait()
	}()

	select {
	case err := <-errChan:
		if err != nil {
			return &RunResult{Completed: false}, err
		}
		return &RunResult{Completed: true}, nil
	case <-ctx.Done():
		err = ctx.Err()
		if err != nil {
			return &RunResult{Completed: false}, err
		}
		return &RunResult{Completed: true}, nil
	}
}

func (i *localInstance) Stop(ctx gocontext.Context) error {
	return nil
}

func (i *localInstance) ID() string {
	return fmt.Sprintf("local:%s", i.scriptPath)
}

func (i *localInstance) StartupDuration() time.Duration { return zeroDuration }
