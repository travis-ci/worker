package ssh

import (
	"bytes"
	"code.google.com/p/go.crypto/ssh"
	"fmt"
	"io"
	"net"
	"time"
)

type Connection struct {
	client *ssh.ClientConn
}

func NewConnection(addr, user string, auths []AuthenticationMethod) (*Connection, error) {
	dial := func(addr string) (*ssh.ClientConn, error) {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return nil, err
		}

		return ssh.Client(conn, &ssh.ClientConfig{
			User: user,
			Auth: toSSHClientAuths(auths),
		})
	}

	var client *ssh.ClientConn
	var err error
	for i := 0; i < 3; i++ {
		client, err = dial(addr)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}

	return &Connection{client: client}, err
}

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

func (c *Connection) Run(cmd string) error {
	session, err := c.createSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

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
