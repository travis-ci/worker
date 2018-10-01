package remote

import "io"

type Remoter interface {
	UploadFile(path string, data []byte) (bool, error)
	DownloadFile(path string) ([]byte, error)
	RunCommand(command string, output io.Writer) (uint8, error)
	Close() error
}
