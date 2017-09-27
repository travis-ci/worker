package winrm

import (
	"bytes"
	"io"

	"github.com/masterzen/winrm"
	"github.com/packer-community/winrmcp/winrmcp"
)

func New(host string, port int, username string, password string) (*Remoter, error) {

	endpoint := winrm.NewEndpoint(host, port, true, true, nil, nil, nil, 0)
	winrmClient, err := winrm.NewClient(endpoint, username, password)
	if err != nil {
		return nil, err
	}
	winrmcpClient, err := winrmcp.New(host, &winrmcp.Config{
		Auth: winrmcp.Auth{
			User:     username,
			Password: password,
		},
		Https:                 true,
		Insecure:              true,
		OperationTimeout:      180,
		MaxOperationsPerShell: 15,
		TransportDecorator:    nil})
	if err != nil {
		return nil, err
	}
	return &Remoter{
		winrmClient:   winrmClient,
		winrmcpClient: winrmcpClient,
	}, nil
}

type Remoter struct {
	winrmClient   *winrm.Client
	winrmcpClient *winrmcp.Winrmcp
	endpoint      *winrm.Endpoint
}

func (r *Remoter) UploadFile(path string, data []byte) (bool, error) {
	err := r.winrmcpClient.Write(path, bytes.NewReader(data))
	if err != nil {
		return false, err
	}
	return true, nil
}
func (r *Remoter) RunCommand(command string, output io.Writer) (uint8, error) {
	exitCode, err := r.winrmClient.Run(command, output, output)
	return uint8(exitCode), err
}

func (r *Remoter) Close() error {
	return nil
}
