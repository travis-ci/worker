package remote

import (
	"io"
	"os"
)

type Remoter interface {
	UploadFile(path string, data []byte) (bool, error)
	DownloadFile(path string) ([]byte, error)
	RunCommand(command string, output io.Writer) (int32, error)
	Chmod(path string, mode os.FileMode) error
	Close() error
}
