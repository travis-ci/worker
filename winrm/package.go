package winrm

import (
	"bytes"
	"fmt"
	"io"

	"github.com/masterzen/winrm"
	"github.com/packer-community/winrmcp/winrmcp"
)

var errNotImplemented = fmt.Errorf("method not implemented")

func New(host string, port int, username, password string) (*Remoter, error) {

	endpoint := winrm.NewEndpoint(host, port, true, true, nil, nil, nil, 0)
	winrmClient, err := winrm.NewClient(endpoint, username, password)
	if err != nil {
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

func (r *Remoter) RunCommand(command string, output io.Writer) (uint8, error) {
	exitCode, err := r.winrmClient.Run(command, output, output)
	return uint8(exitCode), err
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
		OperationTimeout:      180,
		MaxOperationsPerShell: 15,
		TransportDecorator:    nil,
	}

	return winrmcp.New(addr, config)
}
