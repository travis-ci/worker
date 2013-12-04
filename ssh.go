package main

import (
	"bytes"
	"code.google.com/p/go.crypto/ssh"
	"fmt"
	"io"
)

// An SSHConnection manages an SSH connection to a server.
type SSHConnection struct {
	client *ssh.ClientConn
}

type singlePassword struct {
	password string
}

func (pw singlePassword) Password(user string) (string, error) {
	return pw.password, nil
}

// NewSSHConnection creates an SSH connection using the connection information
// for the given server.
func NewSSHConnection(server VMCloudServer) (*SSHConnection, error) {
	sshInfo := server.SSHInfo()
	sshConfig := &ssh.ClientConfig{
		User: sshInfo.Username,
		Auth: []ssh.ClientAuth{
			ssh.ClientAuthPassword(singlePassword{sshInfo.Password}),
		},
	}
	client, err := ssh.Dial("tcp", sshInfo.Addr, sshConfig)
	return &SSHConnection{client: client}, err
}

// Start starts the given command and returns as soon as the command has
// started. It does not wait for the command to finish. The returned channels
// send the stdout of the command and the exit code.
func (c *SSHConnection) Start(cmd string) (<-chan []byte, chan int, error) {
	session, outputChan, err := c.sessionWithOutput()
	if err != nil {
		return nil, nil, err
	}

	err = session.Start(cmd)

	exitCodeChan := make(chan int, 1)
	go func() {
		err := session.Wait()
		if err == nil {
			exitCodeChan <- 0
		} else {
			switch err := err.(type) {
			case *ssh.ExitError:
				exitCodeChan <- err.ExitStatus()
			default:
				exitCodeChan <- 200
			}
		}
		close(exitCodeChan)
	}()

	return outputChan, exitCodeChan, err
}

// Run runs a command and blocks until the command has finished. An error is
// returned if the command exited with a non-zero command.
func (c *SSHConnection) Run(cmd string) error {
	session, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

func (c *SSHConnection) sessionWithOutput() (*ssh.Session, <-chan []byte, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, nil, err
	}

	outputChan := make(chan []byte)
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	go func() {
		copyChan(outputChan, stdout, nil)
		session.Close()
	}()

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})

	return session, outputChan, err
}

// UploadFile uploads the given content to the file on the remote server given
// by the path.
func (c *SSHConnection) UploadFile(path string, content []byte) error {
	session, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}

	go func() {
		io.Copy(stdin, bytes.NewReader(content))
		stdin.Close()
	}()

	return session.Run(fmt.Sprintf("cat > %s", path))
}

func copyChan(outputChan chan []byte, reader io.Reader, errChan chan error) {
	for {
		bytes := make([]byte, 2048)
		n, err := reader.Read(bytes)
		if n > 0 {
			outputChan <- bytes[0:n]
		}
		if err != nil {
			close(outputChan)
			if errChan != nil {
				errChan <- err
			}
			return
		}
	}
}

// Close closes the SSH connection.
func (c *SSHConnection) Close() {
	c.client.Close()
}
