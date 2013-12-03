package main

import (
	"bytes"
	"code.google.com/p/go.crypto/ssh"
	"fmt"
	"io"
)

type SSHConnection struct {
	client *ssh.ClientConn
}

type singlePassword struct {
	password string
}

func (pw singlePassword) Password(user string) (string, error) {
	return pw.password, nil
}

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

func (c *SSHConnection) Start(cmd string) (chan []byte, chan bool, error) {
	session, outputChan, err := c.sessionWithOutput()
	if err != nil {
		return nil, nil, err
	}

	closeChan := make(chan bool)
	go func() {
		<-closeChan
		session.Close()
	}()

	return outputChan, closeChan, session.Start(cmd)
}

func (c *SSHConnection) Run(cmd string) error {
	session, err := c.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	return session.Run(cmd)
}

func (c *SSHConnection) sessionWithOutput() (*ssh.Session, chan []byte, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, nil, err
	}

	outputChan := make(chan []byte)
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	copyChan(outputChan, stdout)
	copyChan(outputChan, stderr)

	err = session.RequestPty("xterm", 80, 40, ssh.TerminalModes{})

	return session, outputChan, err
}

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

func copyChan(outputChan chan []byte, reader io.Reader) chan error {
	errChan := make(chan error, 1)

	go func() {
		for {
			bytes := make([]byte, 2048)
			n, err := reader.Read(bytes)
			if n > 0 {
				outputChan <- bytes[0:n]
			}
			if err != nil {
				close(outputChan)
				errChan <- err
				return
			}
		}
	}()

	return errChan
}

func (c *SSHConnection) Close() {
	c.client.Close()
}
