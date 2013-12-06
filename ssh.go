package main

import (
	"bytes"
	"code.google.com/p/go.crypto/ssh"
	"fmt"
	"io"
	"log"
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
func NewSSHConnection(server VM) (*SSHConnection, error) {
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
// started. It does not wait for the command to finish. The returned channel
// will send the exit code and then close when the command is finished. If the
// exit code sent is -1 then there was an error running the build.
func (c *SSHConnection) Start(cmd string, output io.Writer) (<-chan int, error) {
	session, err := c.createSession()
	if err != nil {
		return nil, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Start(cmd)

	exitCodeChan := make(chan int, 1)
	go func() {
		err := session.Wait()
		closeErr := session.Close()
		log.Printf("SSHConnection.Start: An error occurred while closing the session: %v\n", closeErr)
		if err == nil {
			exitCodeChan <- 0
		} else {
			switch err := err.(type) {
			case *ssh.ExitError:
				if err.ExitStatus() != 0 {
					exitCodeChan <- err.ExitStatus()
				} else {
					log.Printf("SSHConnection.Start: An error occurred while running the command: %v\n", err)
					exitCodeChan <- -1
				}
			default:
				log.Printf("SSHConnection.Start: An error occurred while running the command: %v\n", err)
				exitCodeChan <- -1
			}
		}
		close(exitCodeChan)
	}()

	return exitCodeChan, err
}

// Run runs a command and blocks until the command has finished. An error is
// returned if the command exited with a non-zero command.
func (c *SSHConnection) Run(cmd string) error {
	session, err := c.createSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

func (c *SSHConnection) createSession() (*ssh.Session, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, err
	}

	return session, session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
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

// Close closes the SSH connection.
func (c *SSHConnection) Close() {
	c.client.Close()
}
