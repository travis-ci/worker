package ssh

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"code.google.com/p/go.crypto/ssh"
)

// Connection represents a connection to an SSH server.
type Connection struct {
	client *ssh.Client
}

// NewConnection opens a new connection to an SSH server with the given address
// and authenticates as the user with the given username and authentication
// methods. The connection is then returned.
func NewConnection(addr, user string, auths []AuthenticationMethod) (*Connection, error) {
	var client *ssh.Client
	var err error
	for i := 0; i < 3; i++ {
		client, err = ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User: user,
			Auth: toSSHClientAuths(auths),
		})
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}

	return &Connection{client: client}, err
}

// Start runs the given command, streaming stdout and stderr to the given
// io.Writer. The exit code is sent to the returned channel, or -1 if an error
// occurred while running the command.
func (c *Connection) Start(cmd string, output io.Writer) (<-chan int, error) {
	session, err := c.createSession()
	if err != nil {
		return nil, err
	}

	session.Stdout = output
	session.Stderr = output

	err = session.Start(cmd)

	exitCodeChan := make(chan int, 1)
	go func() {
		defer session.Close()
		err := session.Wait()
		if err == nil {
			exitCodeChan <- 0
		} else {
			switch err := err.(type) {
			case *ssh.ExitError:
				if err.ExitStatus() != 0 {
					exitCodeChan <- err.ExitStatus()
				} else {
					exitCodeChan <- -1
				}
			default:
				exitCodeChan <- -1
			}
		}
		close(exitCodeChan)
	}()

	return exitCodeChan, err
}

// Run runs the given command on the server. An error is returned if the
// command didn't complete successfully (returned exit code 0).
func (c *Connection) Run(cmd string) error {
	session, err := c.createSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

// UploadFile uploads a file with the given content to the given path on the
// server. The 'cat' command must be available on the server.
func (c *Connection) UploadFile(path string, content []byte) error {
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

// Close closes the connection.
func (c *Connection) Close() error {
	return c.client.Close()
}

func (c *Connection) createSession() (*ssh.Session, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, err
	}

	return session, session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})
}
