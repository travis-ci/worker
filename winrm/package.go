package winrm

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/masterzen/winrm"
	"github.com/packer-community/winrmcp/winrmcp"
)

var errNotImplemented = fmt.Errorf("method not implemented")

func New(host string, port int, username, password string) (*Remoter, error) {

	endpoint := &winrm.Endpoint{
		Host:     host,
		Port:     port,
		HTTPS:    true,
		Insecure: true,
	}

	params := *winrm.DefaultParameters
	params.Timeout = "PT2H"

	winrmClient, err := winrm.NewClientWithParameters(
		endpoint, username, password, &params)
	if err != nil {
		return nil, err
	}

	shell, err := winrmClient.CreateShell()
	if err != nil {
		return nil, err
	}

	if err := shell.Close(); err != nil {
		return nil, err
	}

	return &Remoter{
		username:    username,
		password:    password,
		winrmClient: winrmClient,
		endpoint:    endpoint,
	}, nil
}

type Remoter struct {
	username    string
	password    string
	winrmClient *winrm.Client
	endpoint    *winrm.Endpoint
}

func (r *Remoter) UploadFile(path string, data []byte) (bool, error) {
	wcp, err := r.newCopyClient()
	if err != nil {
		return false, err
	}

	err = wcp.Write(path, bytes.NewReader(data))
	return false, err
}

func (r *Remoter) DownloadFile(path string) ([]byte, error) {
	return nil, errNotImplemented
}

func (r *Remoter) RunCommand(command string, output io.Writer) (int32, error) {
	exitCode, err := r.winrmClient.Run(command, output, output)
	return int32(exitCode), err
}

func (r *Remoter) Close() error {
	return nil
}

func (r *Remoter) newCopyClient() (*winrmcp.Winrmcp, error) {
	addr := fmt.Sprintf("%s:%d", r.endpoint.Host, r.endpoint.Port)

	config := &winrmcp.Config{
		Auth: winrmcp.Auth{
			User:     r.username,
			Password: r.password,
		},
		Https:                 true,
		Insecure:              true,
		OperationTimeout:      180 * time.Second,
		MaxOperationsPerShell: 30,
		TransportDecorator:    nil,
	}

	return winrmcp.New(addr, config)
}
