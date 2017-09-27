package winrm

import (
	"io"

	"github.com/masterzen/winrm"
)

func New(host string, port int, username string, password string) (*Remoter, error) {
	endpoint := winrm.NewEndpoint(host, port, true, true, nil, nil, nil, 0)
	client, err := winrm.NewClient(endpoint, username, password)
	if err != nil {
		return nil, err
	}
	return &Remoter{client: client}, nil
}

type Remoter struct {
	client *winrm.Client
}

func (r *Remoter) UploadFile(path string, data []byte) (bool, error) {
	exitCode, err := r.client.Run(command, output, output)
	return true, nil
}
func (r *Remoter) RunCommand(command string, output io.Writer) (uint8, error) {
	exitCode, err := r.client.Run(command, output, output)
	return uint8(exitCode), err
}

func (r *Remoter) Close() error {
	return nil
}
